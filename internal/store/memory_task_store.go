package store

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
)

type MemoryTaskStore struct {
	mu                      sync.RWMutex
	tasks                   map[string]*domain.Task
	taskIDsByIdempotencyKey map[string]string
}

func NewMemoryTaskStore() *MemoryTaskStore {
	return &MemoryTaskStore{
		tasks:                   make(map[string]*domain.Task),
		taskIDsByIdempotencyKey: make(map[string]string),
	}
}

func (s *MemoryTaskStore) Create(ctx context.Context, task *domain.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return ErrNilTask
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[task.ID]; exists {
		return ErrTaskAlreadyExists
	}
	if task.IdempotencyKey != "" {
		if _, exists := s.taskIDsByIdempotencyKey[idempotencyIndexKey(task.Workflow, task.IdempotencyKey)]; exists {
			return ErrIdempotencyConflict
		}
	}

	s.tasks[task.ID] = cloneTask(task)
	if task.IdempotencyKey != "" {
		s.taskIDsByIdempotencyKey[idempotencyIndexKey(task.Workflow, task.IdempotencyKey)] = task.ID
	}
	return nil
}

func idempotencyIndexKey(workflow, key string) string {
	return workflow + "\x00" + key
}

func (s *MemoryTaskStore) Get(ctx context.Context, id string) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, ErrTaskNotFound
	}

	return cloneTask(task), nil
}

func (s *MemoryTaskStore) Update(ctx context.Context, task *domain.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return ErrNilTask
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stored, exists := s.tasks[task.ID]
	if !exists {
		return ErrTaskNotFound
	}
	if stored.Version != task.Version {
		return ErrTaskConflict
	}
	if stored.IdempotencyKey != task.IdempotencyKey {
		return ErrTaskConflict
	}

	updated := cloneTask(task)
	updated.Version++
	s.tasks[task.ID] = updated
	return nil
}

func (s *MemoryTaskStore) ClaimNext(ctx context.Context, options ClaimOptions) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, _, _, err := s.claimNextLocked(options)
	return task, err
}

// claimNextLocked 要求调用方已经持有 s.mu 的写锁。
func (s *MemoryTaskStore) claimNextLocked(
	options ClaimOptions,
) (*domain.Task, *domain.Task, domain.ClaimKind, error) {
	var selected *domain.Task
	claimKind := domain.ClaimInitial
	for _, task := range s.tasks {
		if options.Workflow != "" && task.Workflow != options.Workflow {
			continue
		}
		if task.Status != domain.TaskStatusRunning ||
			task.LeaseExpiresAt == nil ||
			task.LeaseExpiresAt.After(options.Now) ||
			task.RetryCount >= task.MaxRetries {
			continue
		}
		if selected == nil ||
			task.LeaseExpiresAt.Before(*selected.LeaseExpiresAt) ||
			(task.LeaseExpiresAt.Equal(*selected.LeaseExpiresAt) && task.ID < selected.ID) {
			selected = task
			claimKind = domain.ClaimRecovery
		}
	}

	if selected == nil {
		for _, task := range s.tasks {
			if options.Workflow != "" && task.Workflow != options.Workflow {
				continue
			}
			if (task.Status != domain.TaskStatusQueued && task.Status != domain.TaskStatusRetrying) ||
				task.AvailableAt.After(options.Now) {
				continue
			}
			// 与 MySQL 的 ORDER BY available_at, created_at, id 保持一致。
			if selected == nil ||
				task.AvailableAt.Before(selected.AvailableAt) ||
				(task.AvailableAt.Equal(selected.AvailableAt) && task.CreatedAt.Before(selected.CreatedAt)) ||
				(task.AvailableAt.Equal(selected.AvailableAt) &&
					task.CreatedAt.Equal(selected.CreatedAt) &&
					task.ID < selected.ID) {
				selected = task
				if task.Status == domain.TaskStatusRetrying {
					claimKind = domain.ClaimRetry
				} else {
					claimKind = domain.ClaimInitial
				}
			}
		}
	}
	if selected == nil {
		return nil, nil, "", ErrNoTaskAvailable
	}
	previous := cloneTask(selected)
	if claimKind == domain.ClaimRecovery {
		selected.RetryCount++
		selected.UpdatedAt = options.Now
	} else {
		if err := selected.MoveTo(domain.TaskStatusRunning, options.Now); err != nil {
			return nil, nil, "", err
		}
	}
	leaseExpiresAt := options.Now.Add(options.LeaseDuration)
	selected.LeaseOwner = options.WorkerID
	selected.LeaseExpiresAt = &leaseExpiresAt
	selected.LastHeartbeatAt = nil
	selected.Version++

	return cloneTask(selected), previous, claimKind, nil
}

func (s *MemoryTaskStore) RenewLease(ctx context.Context, options RenewLeaseOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[options.TaskID]
	if !exists ||
		task.Status != domain.TaskStatusRunning ||
		task.LeaseOwner != options.WorkerID ||
		task.LeaseExpiresAt == nil ||
		!task.LeaseExpiresAt.After(options.Now) {
		return ErrLeaseLost
	}

	leaseExpiresAt := options.Now.Add(options.LeaseDuration)
	task.LeaseExpiresAt = &leaseExpiresAt
	heartbeatAt := options.Now
	task.LastHeartbeatAt = &heartbeatAt
	task.UpdatedAt = options.Now
	return nil
}

func (s *MemoryTaskStore) FailNextExpired(ctx context.Context, now time.Time) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, ErrInvalidCleanupTime
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, _, err := s.failNextExpiredLocked(now)
	return task, err
}

// failNextExpiredLocked 要求调用方已经持有 s.mu 的写锁。
func (s *MemoryTaskStore) failNextExpiredLocked(now time.Time) (*domain.Task, *domain.Task, error) {
	var selected *domain.Task
	for _, task := range s.tasks {
		if task.Status != domain.TaskStatusRunning ||
			task.LeaseExpiresAt == nil ||
			task.LeaseExpiresAt.After(now) ||
			task.RetryCount < task.MaxRetries {
			continue
		}
		if selected == nil ||
			task.LeaseExpiresAt.Before(*selected.LeaseExpiresAt) ||
			(task.LeaseExpiresAt.Equal(*selected.LeaseExpiresAt) && task.ID < selected.ID) {
			selected = task
		}
	}
	if selected == nil {
		return nil, nil, ErrNoExpiredTask
	}

	previous := cloneTask(selected)
	selected.ErrorMessage = "task lease expired and retry budget exhausted"
	if err := selected.MoveTo(domain.TaskStatusFailed, now); err != nil {
		return nil, nil, err
	}
	selected.Version++
	return cloneTask(selected), previous, nil
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}

	copied := *task
	copied.Input = cloneRawMessage(task.Input)
	copied.Result = cloneRawMessage(task.Result)
	if task.StartedAt != nil {
		startedAt := *task.StartedAt
		copied.StartedAt = &startedAt
	}
	if task.FinishedAt != nil {
		finishedAt := *task.FinishedAt
		copied.FinishedAt = &finishedAt
	}
	if task.LeaseExpiresAt != nil {
		leaseExpiresAt := *task.LeaseExpiresAt
		copied.LeaseExpiresAt = &leaseExpiresAt
	}
	if task.LastHeartbeatAt != nil {
		lastHeartbeatAt := *task.LastHeartbeatAt
		copied.LastHeartbeatAt = &lastHeartbeatAt
	}

	return &copied
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
