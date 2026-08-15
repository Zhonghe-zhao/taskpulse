package domain

import (
	"encoding/json"
	"errors"
	"time"
)

type EventType string

const (
	EventTaskCreated      EventType = "task_created"
	EventTaskStarted      EventType = "task_started"
	EventTaskRetryStarted EventType = "task_retry_started"
	EventTaskRecovered    EventType = "task_recovered"
	EventTaskRetrying     EventType = "task_retrying"
	EventTaskReleased     EventType = "task_released"
	EventTaskProgress     EventType = "task_progress"
	EventTaskSucceeded    EventType = "task_succeeded"
	EventTaskPartial      EventType = "task_partially_succeeded"
	EventTaskFailed       EventType = "task_failed"
	EventTaskCanceled     EventType = "task_canceled"
	EventItemStarted      EventType = "item_started"
	EventItemSucceeded    EventType = "item_succeeded"
	EventItemFailed       EventType = "item_failed"
	EventItemRetrying     EventType = "item_retrying"
)

type TaskEvent struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	Type      EventType       `json:"type"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Progress  int             `json:"progress"`
	CreatedAt time.Time       `json:"created_at"`
}

func NewTaskEvent(id, taskID string, eventType EventType, message string, payload json.RawMessage, progress int, now time.Time) (*TaskEvent, error) {
	if id == "" {
		return nil, errors.New("event id is required")
	}
	if taskID == "" {
		return nil, errors.New("task id is required")
	}
	if eventType == "" {
		return nil, errors.New("event type is required")
	}
	if progress < 0 || progress > 100 {
		return nil, errors.New("progress must be between 0 and 100")
	}
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	return &TaskEvent{
		ID:        id,
		TaskID:    taskID,
		Type:      eventType,
		Message:   message,
		Payload:   payload,
		Progress:  progress,
		CreatedAt: now,
	}, nil
}

func NewTaskClaimedEvent(id string, task *Task, claimKind ClaimKind, now time.Time) (*TaskEvent, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}

	var eventType EventType
	var message string
	switch claimKind {
	case ClaimInitial:
		eventType = EventTaskStarted
		message = "task started"
	case ClaimRetry:
		eventType = EventTaskRetryStarted
		message = "task retry started"
	case ClaimRecovery:
		eventType = EventTaskRecovered
		message = "task recovered after lease expiration"
	default:
		return nil, errors.New("invalid task claim kind")
	}

	payload, err := json.Marshal(struct {
		WorkerID   string     `json:"worker_id"`
		LeaseUntil *time.Time `json:"lease_until,omitempty"`
		RetryCount int        `json:"retry_count"`
	}{
		WorkerID:   task.LeaseOwner,
		LeaseUntil: task.LeaseExpiresAt,
		RetryCount: task.RetryCount,
	})
	if err != nil {
		return nil, err
	}
	return NewTaskEvent(
		id,
		task.ID,
		eventType,
		message,
		payload,
		task.Progress,
		now,
	)
}

func NewTaskExpiredEvent(id string, task *Task, now time.Time) (*TaskEvent, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}

	return NewTaskEvent(
		id,
		task.ID,
		EventTaskFailed,
		"task failed after lease expiration",
		json.RawMessage(`{"reason":"retry_budget_exhausted"}`),
		task.Progress,
		now,
	)
}

func NewTaskReleasedEvent(id string, task *Task, now time.Time) (*TaskEvent, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	if task.Status != TaskStatusQueued {
		return nil, errors.New("task is not queued after release")
	}
	return NewTaskEvent(
		id,
		task.ID,
		EventTaskReleased,
		"task released by worker during graceful shutdown",
		json.RawMessage(`{"reason":"worker_shutdown"}`),
		task.Progress,
		now,
	)
}

func NewTaskCanceledEvent(id string, task *Task, now time.Time) (*TaskEvent, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	if task.Status != TaskStatusCanceled {
		return nil, errors.New("task is not canceled")
	}

	return NewTaskEvent(
		id,
		task.ID,
		EventTaskCanceled,
		"task canceled",
		json.RawMessage(`{"reason":"requested_by_caller"}`),
		task.Progress,
		now,
	)
}

func NewTaskRetryingEvent(
	id string,
	task *Task,
	errorCode string,
	delay time.Duration,
	now time.Time,
) (*TaskEvent, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	if task.Status != TaskStatusRetrying {
		return nil, errors.New("task is not retrying")
	}
	if errorCode == "" {
		return nil, errors.New("retry error code is required")
	}
	if delay <= 0 {
		return nil, errors.New("retry delay must be positive")
	}

	payload, err := json.Marshal(struct {
		ErrorCode   string    `json:"error_code"`
		RetryCount  int       `json:"retry_count"`
		MaxRetries  int       `json:"max_retries"`
		AvailableAt time.Time `json:"available_at"`
		DelayMillis int64     `json:"delay_ms"`
	}{
		ErrorCode:   errorCode,
		RetryCount:  task.RetryCount,
		MaxRetries:  task.MaxRetries,
		AvailableAt: task.AvailableAt,
		DelayMillis: delay.Milliseconds(),
	})
	if err != nil {
		return nil, err
	}

	return NewTaskEvent(
		id,
		task.ID,
		EventTaskRetrying,
		"task scheduled for retry",
		payload,
		task.Progress,
		now,
	)
}
