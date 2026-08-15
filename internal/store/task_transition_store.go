package store

import (
	"context"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

type TaskTransitionStore interface {
	ClaimNextWithEvent(ctx context.Context, options ClaimOptions, eventID string) (*domain.Task, error)
	FailNextExpiredWithEvent(ctx context.Context, now time.Time, eventID string) (*domain.Task, error)
	UpdateTaskWithEvent(ctx context.Context, task *domain.Task, event *domain.TaskEvent) error
}

type MemoryTaskTransitionStore struct {
	taskStore  *MemoryTaskStore
	eventStore *MemoryEventStore
}

var _ TaskTransitionStore = (*MemoryTaskTransitionStore)(nil)

func NewMemoryTaskTransitionStore(
	taskStore *MemoryTaskStore,
	eventStore *MemoryEventStore,
) *MemoryTaskTransitionStore {
	return &MemoryTaskTransitionStore{
		taskStore:  taskStore,
		eventStore: eventStore,
	}
}

func (s *MemoryTaskTransitionStore) ClaimNextWithEvent(
	ctx context.Context,
	options ClaimOptions,
	eventID string,
) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}

	s.taskStore.mu.Lock()
	defer s.taskStore.mu.Unlock()
	s.eventStore.mu.Lock()
	defer s.eventStore.mu.Unlock()

	if _, exists := s.eventStore.eventsByID[eventID]; exists {
		return nil, ErrEventAlreadyExists
	}

	task, previous, claimKind, err := s.taskStore.claimNextLocked(options)
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskClaimedEvent(eventID, task, claimKind, options.Now)
	if err != nil {
		s.taskStore.tasks[previous.ID] = previous
		return nil, err
	}

	copiedEvent := cloneTaskEvent(event)
	s.eventStore.eventsByID[event.ID] = copiedEvent
	s.eventStore.eventsByTaskID[event.TaskID] = append(
		s.eventStore.eventsByTaskID[event.TaskID],
		copiedEvent,
	)
	return task, nil
}

func (s *MemoryTaskTransitionStore) FailNextExpiredWithEvent(
	ctx context.Context,
	now time.Time,
	eventID string,
) (*domain.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, ErrInvalidCleanupTime
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}

	s.taskStore.mu.Lock()
	defer s.taskStore.mu.Unlock()
	s.eventStore.mu.Lock()
	defer s.eventStore.mu.Unlock()

	if _, exists := s.eventStore.eventsByID[eventID]; exists {
		return nil, ErrEventAlreadyExists
	}

	task, previous, err := s.taskStore.failNextExpiredLocked(now)
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskExpiredEvent(eventID, task, now)
	if err != nil {
		s.taskStore.tasks[previous.ID] = previous
		return nil, err
	}

	copiedEvent := cloneTaskEvent(event)
	s.eventStore.eventsByID[event.ID] = copiedEvent
	s.eventStore.eventsByTaskID[event.TaskID] = append(
		s.eventStore.eventsByTaskID[event.TaskID],
		copiedEvent,
	)
	return task, nil
}

func (s *MemoryTaskTransitionStore) UpdateTaskWithEvent(
	ctx context.Context,
	task *domain.Task,
	event *domain.TaskEvent,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return ErrNilTask
	}
	if event == nil {
		return ErrNilEvent
	}
	if event.TaskID != task.ID {
		return ErrTaskEventMismatch
	}

	s.taskStore.mu.Lock()
	defer s.taskStore.mu.Unlock()
	s.eventStore.mu.Lock()
	defer s.eventStore.mu.Unlock()

	stored, exists := s.taskStore.tasks[task.ID]
	if !exists {
		return ErrTaskNotFound
	}
	if stored.Version != task.Version {
		return ErrTaskConflict
	}
	if _, exists := s.eventStore.eventsByID[event.ID]; exists {
		return ErrEventAlreadyExists
	}

	updatedTask := cloneTask(task)
	updatedTask.Version++
	copiedEvent := cloneTaskEvent(event)
	s.taskStore.tasks[task.ID] = updatedTask
	s.eventStore.eventsByID[event.ID] = copiedEvent
	s.eventStore.eventsByTaskID[event.TaskID] = append(
		s.eventStore.eventsByTaskID[event.TaskID],
		copiedEvent,
	)
	return nil
}
