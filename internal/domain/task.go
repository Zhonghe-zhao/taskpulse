package domain

import (
	"encoding/json"
	"errors"
	"time"
)

type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusSucceeded TaskStatus = "succeeded"
	TaskStatusPartial   TaskStatus = "partially_succeeded"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCanceled  TaskStatus = "canceled"
	TaskStatusRetrying  TaskStatus = "retrying"
)

var terminalStatuses = map[TaskStatus]bool{
	TaskStatusSucceeded: true,
	TaskStatusPartial:   true,
	TaskStatusFailed:    true,
	TaskStatusCanceled:  true,
}

var (
	ErrRetryBudgetExhausted = errors.New("task retry budget exhausted")
	ErrInvalidRetryTime     = errors.New("retry time must be after current time")
)

type Task struct {
	ID              string          `json:"id"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Workflow        string          `json:"workflow"`
	Status          TaskStatus      `json:"status"`
	Input           json.RawMessage `json:"input"`
	Result          json.RawMessage `json:"result,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	Progress        int             `json:"progress"`
	RetryCount      int             `json:"retry_count"`
	MaxRetries      int             `json:"max_retries"`
	AvailableAt     time.Time       `json:"available_at"`
	Version         uint64          `json:"version"`
	LeaseOwner      string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty"`
	LastHeartbeatAt *time.Time      `json:"last_heartbeat_at,omitempty"`
	// LeaseToken is an external protocol token derived when a task is claimed.
	// The durable fencing state remains LeaseOwner, LeaseExpiresAt, and Version.
	LeaseToken string     `json:"lease_token,omitempty"`
	TaskID     string     `json:"task_id,omitempty"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func NewTask(id, workflow string, input json.RawMessage, maxRetries int, now time.Time) (*Task, error) {
	if id == "" {
		return nil, errors.New("task id is required")
	}
	if workflow == "" {
		return nil, errors.New("workflow is required")
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if maxRetries < 0 {
		return nil, errors.New("max retries cannot be negative")
	}

	return &Task{
		ID:          id,
		Workflow:    workflow,
		Status:      TaskStatusQueued,
		Input:       input,
		Progress:    0,
		MaxRetries:  maxRetries,
		AvailableAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (t *Task) MoveTo(next TaskStatus, now time.Time) error {
	if t == nil {
		return errors.New("task is nil")
	}
	if !CanTransition(t.Status, next) {
		return errors.New("invalid task status transition")
	}

	t.Status = next
	t.UpdatedAt = now

	switch next {
	case TaskStatusRunning:
		if t.StartedAt == nil {
			t.StartedAt = &now
		}
	case TaskStatusRetrying:
		t.LeaseOwner = ""
		t.LeaseExpiresAt = nil
	case TaskStatusSucceeded, TaskStatusPartial, TaskStatusFailed, TaskStatusCanceled:
		t.FinishedAt = &now
		t.LeaseOwner = ""
		t.LeaseExpiresAt = nil
	}

	return nil
}

func (t *Task) ScheduleRetry(now, availableAt time.Time) error {
	if t == nil {
		return errors.New("task is nil")
	}
	if !availableAt.After(now) {
		return ErrInvalidRetryTime
	}
	if t.RetryCount >= t.MaxRetries {
		return ErrRetryBudgetExhausted
	}
	if err := t.MoveTo(TaskStatusRetrying, now); err != nil {
		return err
	}

	t.RetryCount++
	t.AvailableAt = availableAt
	return nil
}

// RequeueForRelease returns an actively running task to the queue without
// consuming retry budget. It is used when a Worker is shutting down cleanly;
// crash recovery remains the fallback when the Worker cannot report release.
func (t *Task) RequeueForRelease(now time.Time) error {
	if t == nil {
		return errors.New("task is nil")
	}
	if t.Status != TaskStatusRunning {
		return errors.New("only running tasks can be released")
	}
	if now.IsZero() {
		return errors.New("release time is required")
	}
	t.Status = TaskStatusQueued
	t.Progress = 0
	t.ErrorMessage = ""
	t.AvailableAt = now
	t.LeaseOwner = ""
	t.LeaseExpiresAt = nil
	t.UpdatedAt = now
	return nil
}

func (t *Task) IsTerminal() bool {
	if t == nil {
		return false
	}
	return terminalStatuses[t.Status]
}

func CanTransition(from, to TaskStatus) bool {
	switch from {
	case TaskStatusQueued:
		return to == TaskStatusRunning || to == TaskStatusCanceled
	case TaskStatusRunning:
		return to == TaskStatusSucceeded || to == TaskStatusPartial || to == TaskStatusFailed || to == TaskStatusCanceled || to == TaskStatusRetrying
	case TaskStatusRetrying:
		return to == TaskStatusRunning || to == TaskStatusFailed || to == TaskStatusCanceled
	case TaskStatusSucceeded, TaskStatusPartial, TaskStatusFailed, TaskStatusCanceled:
		return false
	default:
		return false
	}
}
