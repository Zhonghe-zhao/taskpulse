package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewTaskEvent(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)

	event, err := NewTaskEvent("event_1", "task_1", EventTaskCreated, "task created", nil, 0, now)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}

	if event.TaskID != "task_1" {
		t.Fatalf("expected task id task_1, got %s", event.TaskID)
	}
	if event.Type != EventTaskCreated {
		t.Fatalf("expected task_created event, got %s", event.Type)
	}
}

func TestRejectInvalidProgress(t *testing.T) {
	now := time.Date(2026, 6, 11, 15, 0, 0, 0, time.UTC)

	if _, err := NewTaskEvent("event_1", "task_1", EventTaskCreated, "task created", nil, 101, now); err == nil {
		t.Fatalf("expected progress > 100 to be rejected")
	}
}

func TestNewTaskClaimedEventUsesStartedForFirstClaim(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}

	event, err := NewTaskClaimedEvent("event_started", task, ClaimInitial, now)
	if err != nil {
		t.Fatalf("NewTaskClaimedEvent returned error: %v", err)
	}
	if event.Type != EventTaskStarted || event.Message != "task started" {
		t.Fatalf("unexpected first claim event: %+v", event)
	}
}

func TestNewTaskClaimedEventUsesRecoveredForLeaseRecovery(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	event, err := NewTaskClaimedEvent("event_recovered", task, ClaimRecovery, now)
	if err != nil {
		t.Fatalf("NewTaskClaimedEvent returned error: %v", err)
	}
	if event.Type != EventTaskRecovered || event.Message != "task recovered after lease expiration" {
		t.Fatalf("unexpected recovered claim event: %+v", event)
	}
}

func TestNewTaskClaimedEventUsesRetryStartedForScheduledRetry(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}

	event, err := NewTaskClaimedEvent("event_retry_started", task, ClaimRetry, now)
	if err != nil {
		t.Fatalf("NewTaskClaimedEvent returned error: %v", err)
	}
	if event.Type != EventTaskRetryStarted || event.Message != "task retry started" {
		t.Fatalf("unexpected retry claim event: %+v", event)
	}
}

func TestNewTaskExpiredEvent(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "url_check", nil, 0, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}

	event, err := NewTaskExpiredEvent("event_failed", task, now)
	if err != nil {
		t.Fatalf("NewTaskExpiredEvent returned error: %v", err)
	}
	if event.Type != EventTaskFailed ||
		event.Message != "task failed after lease expiration" ||
		string(event.Payload) != `{"reason":"retry_budget_exhausted"}` {
		t.Fatalf("unexpected expired task event: %+v", event)
	}
}

func TestNewTaskCanceledEvent(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusCanceled, now.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo canceled returned error: %v", err)
	}

	event, err := NewTaskCanceledEvent("event_canceled", task, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NewTaskCanceledEvent returned error: %v", err)
	}
	if event.Type != EventTaskCanceled || event.TaskID != task.ID {
		t.Fatalf("unexpected canceled event: %+v", event)
	}
}

func TestNewTaskReleasedEvent(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	if err := task.RequeueForRelease(now.Add(time.Second)); err != nil {
		t.Fatalf("RequeueForRelease returned error: %v", err)
	}
	event, err := NewTaskReleasedEvent("event_1", task, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NewTaskReleasedEvent returned error: %v", err)
	}
	if event.Type != EventTaskReleased || event.Message != "task released by worker during graceful shutdown" {
		t.Fatalf("unexpected release event: %+v", event)
	}
}

func TestNewTaskRetryingEvent(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := NewTask("task_1", "llm_analysis", nil, 3, now)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := task.MoveTo(TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	delay := 2 * time.Second
	if err := task.ScheduleRetry(now, now.Add(delay)); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}

	event, err := NewTaskRetryingEvent(
		"event_retrying",
		task,
		"rate_limited",
		delay,
		now,
	)
	if err != nil {
		t.Fatalf("NewTaskRetryingEvent returned error: %v", err)
	}
	if event.Type != EventTaskRetrying {
		t.Fatalf("expected task_retrying, got %s", event.Type)
	}
	var payload struct {
		ErrorCode   string `json:"error_code"`
		RetryCount  int    `json:"retry_count"`
		DelayMillis int64  `json:"delay_ms"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal payload returned error: %v", err)
	}
	if payload.ErrorCode != "rate_limited" ||
		payload.RetryCount != 1 ||
		payload.DelayMillis != delay.Milliseconds() {
		t.Fatalf("unexpected retry payload: %+v", payload)
	}
}
