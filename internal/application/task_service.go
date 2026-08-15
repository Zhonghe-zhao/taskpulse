package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/identity"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

var (
	ErrInvalidInput = errors.New("invalid input")
)

type CreateTaskInput struct {
	IdempotencyKey string
	Workflow       string
	Input          json.RawMessage
	MaxRetries     int
}

type CreateTaskResult struct {
	Task    *domain.Task
	Created bool
}

type TaskService struct {
	taskStore             store.TaskStore
	taskQueryStore        store.TaskQueryStore
	taskStatsStore        store.FilteredTaskStatsStore
	eventStore            store.EventStore
	taskCreationStore     store.TaskCreationStore
	taskCancellationStore store.TaskCancellationStore
	now                   func() time.Time
}

func NewTaskService(
	taskStore store.TaskStore,
	eventStore store.EventStore,
	taskCreationStore store.TaskCreationStore,
	taskCancellationStore store.TaskCancellationStore,
) *TaskService {
	service := &TaskService{
		taskStore:             taskStore,
		eventStore:            eventStore,
		taskCreationStore:     taskCreationStore,
		taskCancellationStore: taskCancellationStore,
		now:                   time.Now,
	}
	if queryStore, ok := taskStore.(store.TaskQueryStore); ok {
		service.taskQueryStore = queryStore
	}
	if statsStore, ok := taskStore.(store.FilteredTaskStatsStore); ok {
		service.taskStatsStore = statsStore
	}
	return service
}

type TaskStatsInput struct {
	Workflow string
	Status   domain.TaskStatus
}

type TaskStatsResult struct {
	Workflow           string                        `json:"workflow,omitempty"`
	Status             domain.TaskStatus             `json:"status,omitempty"`
	StatusCounts       map[domain.TaskStatus]int     `json:"status_counts"`
	AvailableCounts    map[domain.TaskStatus]int     `json:"available_counts"`
	OldestAvailableAge map[domain.TaskStatus]float64 `json:"oldest_available_age_seconds"`
}

type TaskDetailResult struct {
	ID                  string            `json:"id"`
	IdempotencyKey      string            `json:"idempotency_key,omitempty"`
	Workflow            string            `json:"workflow"`
	Status              domain.TaskStatus `json:"status"`
	Input               json.RawMessage   `json:"input"`
	Result              json.RawMessage   `json:"result,omitempty"`
	ResultRef           json.RawMessage   `json:"result_ref,omitempty"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	ErrorCode           string            `json:"error_code,omitempty"`
	Retryable           *bool             `json:"retryable,omitempty"`
	Progress            int               `json:"progress"`
	RetryCount          int               `json:"retry_count"`
	MaxRetries          int               `json:"max_retries"`
	AvailableAt         time.Time         `json:"available_at"`
	NextRetryAt         *time.Time        `json:"next_retry_at,omitempty"`
	Version             uint64            `json:"version"`
	WorkerID            string            `json:"worker_id,omitempty"`
	LeaseUntil          *time.Time        `json:"lease_until,omitempty"`
	LastHeartbeatAt     *time.Time        `json:"last_heartbeat_at,omitempty"`
	QueueDurationMS     int64             `json:"queue_duration_ms"`
	ExecutionDurationMS int64             `json:"execution_duration_ms"`
	TotalDurationMS     int64             `json:"total_duration_ms"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	StartedAt           *time.Time        `json:"started_at,omitempty"`
	FinishedAt          *time.Time        `json:"finished_at,omitempty"`
}

func (s *TaskService) GetTaskStats(ctx context.Context, input TaskStatsInput) (*TaskStatsResult, error) {
	if s.taskStatsStore == nil {
		return nil, errors.New("task stats store is not configured")
	}
	if strings.TrimSpace(input.Workflow) != input.Workflow {
		return nil, fmt.Errorf("%w: workflow cannot have surrounding whitespace", ErrInvalidInput)
	}
	filter := store.TaskStatsFilter{Workflow: input.Workflow, Status: input.Status}
	if err := filter.Validate(); err != nil {
		return nil, fmt.Errorf("%w: invalid task stats filter", ErrInvalidInput)
	}
	snapshot, err := s.taskStatsStore.SnapshotFilteredTaskStats(ctx, s.now(), filter)
	if err != nil {
		return nil, err
	}
	ages := make(map[domain.TaskStatus]float64, len(snapshot.OldestAvailableAge))
	for status, age := range snapshot.OldestAvailableAge {
		ages[status] = age.Seconds()
	}
	return &TaskStatsResult{
		Workflow:           input.Workflow,
		Status:             input.Status,
		StatusCounts:       snapshot.StatusCounts,
		AvailableCounts:    snapshot.AvailableCounts,
		OldestAvailableAge: ages,
	}, nil
}

type ListTasksInput struct {
	Workflow string
	Status   domain.TaskStatus
	Cursor   string
	Limit    int
}

type ListTasksResult struct {
	Items      []store.TaskListItem `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
	HasMore    bool                 `json:"has_more"`
}

type taskListCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (s *TaskService) ListTasks(ctx context.Context, input ListTasksInput) (*ListTasksResult, error) {
	if s.taskQueryStore == nil {
		return nil, errors.New("task query store is not configured")
	}
	if strings.TrimSpace(input.Workflow) != input.Workflow {
		return nil, fmt.Errorf("%w: workflow cannot have surrounding whitespace", ErrInvalidInput)
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	if input.Limit < 1 || input.Limit > 100 {
		return nil, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidInput)
	}

	options := store.ListTasksOptions{
		Workflow: input.Workflow,
		Status:   input.Status,
		Limit:    input.Limit + 1,
	}
	if input.Cursor != "" {
		cursor, err := decodeTaskListCursor(input.Cursor)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid cursor", ErrInvalidInput)
		}
		options.BeforeCreatedAt = &cursor.CreatedAt
		options.BeforeID = cursor.ID
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("%w: invalid task query", ErrInvalidInput)
	}

	items, err := s.taskQueryStore.ListTasks(ctx, options)
	if err != nil {
		return nil, err
	}
	result := &ListTasksResult{Items: items}
	if len(items) > input.Limit {
		result.HasMore = true
		result.Items = items[:input.Limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = encodeTaskListCursor(taskListCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return result, nil
}

func encodeTaskListCursor(cursor taskListCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeTaskListCursor(raw string) (taskListCursor, error) {
	var cursor taskListCursor
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return cursor, err
	}
	if cursor.ID == "" || cursor.CreatedAt.IsZero() {
		return cursor, errors.New("cursor is incomplete")
	}
	return cursor, nil
}

func (s *TaskService) CreateTask(ctx context.Context, input CreateTaskInput) (*CreateTaskResult, error) {
	if input.Workflow == "" {
		return nil, fmt.Errorf("%w: workflow is required", ErrInvalidInput)
	}
	if input.MaxRetries < 0 {
		return nil, fmt.Errorf("%w: max retries cannot be negative", ErrInvalidInput)
	}
	if input.IdempotencyKey != "" {
		if strings.TrimSpace(input.IdempotencyKey) != input.IdempotencyKey {
			return nil, fmt.Errorf("%w: idempotency key cannot have surrounding whitespace", ErrInvalidInput)
		}
		if len(input.IdempotencyKey) > 128 {
			return nil, fmt.Errorf("%w: idempotency key exceeds 128 bytes", ErrInvalidInput)
		}
	}

	now := s.now()
	taskID := identity.New("task")
	eventID := identity.New("event")

	task, err := domain.NewTask(taskID, input.Workflow, input.Input, input.MaxRetries, now)
	if err != nil {
		return nil, err
	}
	task.IdempotencyKey = input.IdempotencyKey

	event, err := domain.NewTaskEvent(
		eventID,
		task.ID,
		domain.EventTaskCreated,
		"task created",
		json.RawMessage("{}"),
		task.Progress,
		now,
	)
	if err != nil {
		return nil, err
	}

	creationResult, err := s.taskCreationStore.CreateTaskWithEvent(ctx, task, event)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResult{
		Task:    creationResult.Task,
		Created: creationResult.Created,
	}, nil
}

func (s *TaskService) GetTask(ctx context.Context, taskID string) (*domain.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task id is required", ErrInvalidInput)
	}

	return s.taskStore.Get(ctx, taskID)
}

func (s *TaskService) GetTaskDetail(ctx context.Context, taskID string) (*TaskDetailResult, error) {
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	events, err := s.eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return newTaskDetailResult(task, events, s.now()), nil
}

func newTaskDetailResult(task *domain.Task, events []*domain.TaskEvent, now time.Time) *TaskDetailResult {
	result := &TaskDetailResult{
		ID:              task.ID,
		IdempotencyKey:  task.IdempotencyKey,
		Workflow:        task.Workflow,
		Status:          task.Status,
		Input:           append(json.RawMessage(nil), task.Input...),
		Result:          append(json.RawMessage(nil), task.Result...),
		ResultRef:       append(json.RawMessage(nil), task.Result...),
		ErrorMessage:    task.ErrorMessage,
		Progress:        task.Progress,
		RetryCount:      task.RetryCount,
		MaxRetries:      task.MaxRetries,
		AvailableAt:     task.AvailableAt,
		Version:         task.Version,
		WorkerID:        task.LeaseOwner,
		LeaseUntil:      cloneTimePointer(task.LeaseExpiresAt),
		LastHeartbeatAt: cloneTimePointer(task.LastHeartbeatAt),
		CreatedAt:       task.CreatedAt,
		UpdatedAt:       task.UpdatedAt,
		StartedAt:       cloneTimePointer(task.StartedAt),
		FinishedAt:      cloneTimePointer(task.FinishedAt),
	}
	if task.Status == domain.TaskStatusRetrying {
		result.NextRetryAt = cloneTimePointer(&task.AvailableAt)
	}
	populateFailureDiagnostics(result, events)
	populateWorkerDiagnostics(result, events)
	end := now
	if task.FinishedAt != nil {
		end = *task.FinishedAt
	}
	queueEnd := end
	if task.StartedAt != nil {
		queueEnd = *task.StartedAt
	}
	result.QueueDurationMS = nonNegativeDuration(task.CreatedAt, queueEnd).Milliseconds()
	if task.StartedAt != nil {
		result.ExecutionDurationMS = nonNegativeDuration(*task.StartedAt, end).Milliseconds()
	}
	result.TotalDurationMS = nonNegativeDuration(task.CreatedAt, end).Milliseconds()
	return result
}

func populateWorkerDiagnostics(result *TaskDetailResult, events []*domain.TaskEvent) {
	for index := len(events) - 1; index >= 0; index-- {
		var payload struct {
			WorkerID   string     `json:"worker_id"`
			LeaseUntil *time.Time `json:"lease_until"`
		}
		if json.Unmarshal(events[index].Payload, &payload) != nil {
			continue
		}
		if result.WorkerID == "" && payload.WorkerID != "" {
			result.WorkerID = payload.WorkerID
		}
		if result.LeaseUntil == nil && payload.LeaseUntil != nil {
			result.LeaseUntil = cloneTimePointer(payload.LeaseUntil)
		}
		if result.WorkerID != "" && result.LeaseUntil != nil {
			return
		}
	}
}

func populateFailureDiagnostics(result *TaskDetailResult, events []*domain.TaskEvent) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != domain.EventTaskRetrying && event.Type != domain.EventTaskFailed {
			continue
		}
		var payload struct {
			ErrorCode string `json:"error_code"`
			Retryable *bool  `json:"retryable"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.ErrorCode == "" {
			continue
		}
		result.ErrorCode = payload.ErrorCode
		result.Retryable = payload.Retryable
		if event.Type == domain.EventTaskRetrying && result.Retryable == nil {
			retryable := true
			result.Retryable = &retryable
		}
		return
	}
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func (s *TaskService) CancelTask(ctx context.Context, taskID string) (*domain.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task id is required", ErrInvalidInput)
	}

	result, err := s.taskCancellationStore.CancelTaskWithEvent(
		ctx,
		taskID,
		identity.New("event"),
		s.now(),
	)
	if err != nil {
		return nil, err
	}
	return result.Task, nil
}

func (s *TaskService) ListTaskEvents(ctx context.Context, taskID string) ([]*domain.TaskEvent, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task id is required", ErrInvalidInput)
	}

	if _, err := s.taskStore.Get(ctx, taskID); err != nil {
		return nil, err
	}

	return s.eventStore.ListByTaskID(ctx, taskID)
}
