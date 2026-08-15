package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestNewTaskDefaultsToQueued(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)

	task, err := NewTask("task_1", "url_check", json.RawMessage(`{"urls":["https://example.com"]}`), 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	if task.Status != TaskStatusQueued {
		t.Fatalf("expected queued status, got %s", task.Status)
	}
	if task.Progress != 0 {
		t.Fatalf("expected progress 0, got %d", task.Progress)
	}
	if task.AvailableAt != now || task.CreatedAt != now || task.UpdatedAt != now {
		t.Fatalf("expected timestamps to equal now")
	}
}

func TestTaskStatusTransition(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	if err := task.MoveTo(TaskStatusRunning, now.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	if task.StartedAt == nil {
		t.Fatalf("expected StartedAt to be set")
	}
	leaseExpiresAt := now.Add(time.Minute)
	task.LeaseOwner = "worker_1"
	task.LeaseExpiresAt = &leaseExpiresAt

	if err := task.MoveTo(TaskStatusSucceeded, now.Add(2*time.Second)); err != nil {
		t.Fatalf("MoveTo succeeded returned error: %v", err)
	}
	if !task.IsTerminal() {
		t.Fatalf("expected task to be terminal")
	}
	if task.FinishedAt == nil {
		t.Fatalf("expected FinishedAt to be set")
	}
	if task.LeaseOwner != "" || task.LeaseExpiresAt != nil {
		t.Fatalf("expected terminal task lease to be cleared")
	}
}

func TestRejectInvalidTransition(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	if err := task.MoveTo(TaskStatusSucceeded, now.Add(time.Second)); err == nil {
		t.Fatalf("expected queued -> succeeded to be rejected")
	}
}

func TestRunningTaskCanPartiallySucceed(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	if err := task.MoveTo(TaskStatusRunning, now.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusPartial, now.Add(2*time.Second)); err != nil {
		t.Fatalf("MoveTo partially_succeeded returned error: %v", err)
	}
	if !task.IsTerminal() {
		t.Fatalf("expected partially succeeded task to be terminal")
	}
}

func TestTaskSchedulesRetryAndClearsLease(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	runningAt := now.Add(time.Second)
	if err := task.MoveTo(TaskStatusRunning, runningAt); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	leaseExpiresAt := runningAt.Add(time.Minute)
	task.LeaseOwner = "worker_1"
	task.LeaseExpiresAt = &leaseExpiresAt

	retryAt := runningAt.Add(10 * time.Second)
	if err := task.ScheduleRetry(runningAt, retryAt); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}
	if task.Status != TaskStatusRetrying ||
		task.RetryCount != 1 ||
		!task.AvailableAt.Equal(retryAt) {
		t.Fatalf("unexpected retrying task: %+v", task)
	}
	if task.LeaseOwner != "" || task.LeaseExpiresAt != nil {
		t.Fatalf("expected retrying task lease to be cleared: %+v", task)
	}
}

func TestTaskRequeuesForGracefulReleaseWithoutUsingRetryBudget(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	leaseUntil := now.Add(time.Minute)
	task.LeaseOwner = "worker_1"
	task.LeaseExpiresAt = &leaseUntil
	task.Progress = 60
	task.RetryCount = 2

	releasedAt := now.Add(2 * time.Second)
	if err := task.RequeueForRelease(releasedAt); err != nil {
		t.Fatalf("RequeueForRelease returned error: %v", err)
	}
	if task.Status != TaskStatusQueued || task.Progress != 0 || task.RetryCount != 2 ||
		task.LeaseOwner != "" || task.LeaseExpiresAt != nil || !task.AvailableAt.Equal(releasedAt) {
		t.Fatalf("unexpected released task: %+v", task)
	}
}

func TestTaskRejectsRetryWithoutBudget(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 0, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}

	err = task.ScheduleRetry(now, now.Add(time.Second))
	if !errors.Is(err, ErrRetryBudgetExhausted) {
		t.Fatalf("expected ErrRetryBudgetExhausted, got %v", err)
	}
	if task.Status != TaskStatusRunning || task.RetryCount != 0 {
		t.Fatalf("rejected retry changed task: %+v", task)
	}
}

func TestTaskRejectsRetryTimeThatIsNotInFuture(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 1, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}

	err = task.ScheduleRetry(now, now)
	if !errors.Is(err, ErrInvalidRetryTime) {
		t.Fatalf("expected ErrInvalidRetryTime, got %v", err)
	}
	if task.Status != TaskStatusRunning || task.RetryCount != 0 {
		t.Fatalf("rejected retry changed task: %+v", task)
	}
}
