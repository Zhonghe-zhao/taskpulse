package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

func TestMemoryTaskTransitionStoreClaimsTaskAndAppendsStartedEvent(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := task.CreatedAt.Add(time.Second)
	claimed, err := transitionStore.ClaimNextWithEvent(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}, "event_started")
	if err != nil {
		t.Fatalf("ClaimNextWithEvent returned error: %v", err)
	}
	if claimed.Status != domain.TaskStatusRunning || claimed.Version != 1 {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.EventTaskStarted {
		t.Fatalf("unexpected claim events: %+v", events)
	}
}

func TestMemoryTaskTransitionStoreRollsBackClaimWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	conflictingEvent := newTestEvent(t, "event_conflict", task.ID, domain.EventTaskProgress, 0)
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}

	_, err := transitionStore.ClaimNextWithEvent(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	}, conflictingEvent.ID)
	if !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusQueued || stored.Version != 0 {
		t.Fatalf("task claim was not rolled back: %+v", stored)
	}
}

func TestMemoryTaskTransitionStoreClaimsAvailableRetryingTask(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := task.CreatedAt.Add(time.Second)
	running, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	scheduledAt := claimedAt.Add(time.Second)
	retryAt := scheduledAt.Add(10 * time.Second)
	if err := running.ScheduleRetry(scheduledAt, retryAt); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}
	retryingEvent, err := domain.NewTaskRetryingEvent(
		"event_retrying",
		running,
		"rate_limited",
		retryAt.Sub(scheduledAt),
		scheduledAt,
	)
	if err != nil {
		t.Fatalf("NewTaskRetryingEvent returned error: %v", err)
	}
	if err := transitionStore.UpdateTaskWithEvent(ctx, running, retryingEvent); err != nil {
		t.Fatalf("UpdateTaskWithEvent returned error: %v", err)
	}

	if _, err := transitionStore.ClaimNextWithEvent(ctx, ClaimOptions{
		WorkerID:      "worker_2",
		Now:           retryAt.Add(-time.Nanosecond),
		LeaseDuration: time.Minute,
	}, "event_too_early"); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected ErrNoTaskAvailable before retry time, got %v", err)
	}

	retried, err := transitionStore.ClaimNextWithEvent(ctx, ClaimOptions{
		WorkerID:      "worker_2",
		Now:           retryAt,
		LeaseDuration: time.Minute,
	}, "event_retry_started")
	if err != nil {
		t.Fatalf("ClaimNextWithEvent returned error: %v", err)
	}
	if retried.Status != domain.TaskStatusRunning || retried.RetryCount != 1 {
		t.Fatalf("unexpected retried task: %+v", retried)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 2 || events[1].Type != domain.EventTaskRetryStarted {
		t.Fatalf("unexpected retry event sequence: %+v", events)
	}
}

func TestMemoryTaskTransitionStoreFailsExpiredTaskAndAppendsEvent(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	task.MaxRetries = 0
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	claimedAt := task.CreatedAt.Add(time.Second)
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "crashed_worker",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	expiredAt := claimedAt.Add(time.Minute)
	failed, err := transitionStore.FailNextExpiredWithEvent(ctx, expiredAt, "event_failed")
	if err != nil {
		t.Fatalf("FailNextExpiredWithEvent returned error: %v", err)
	}
	if failed.Status != domain.TaskStatusFailed || failed.Version != 2 {
		t.Fatalf("unexpected failed task: %+v", failed)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.EventTaskFailed {
		t.Fatalf("unexpected expired task events: %+v", events)
	}
}

func TestMemoryTaskTransitionStoreRollsBackExpiredFailureWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	task.MaxRetries = 0
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	claimedAt := task.CreatedAt.Add(time.Second)
	running, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "crashed_worker",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	conflictingEvent := newTestEvent(t, "event_conflict", task.ID, domain.EventTaskProgress, 0)
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}

	_, err = transitionStore.FailNextExpiredWithEvent(
		ctx,
		claimedAt.Add(time.Minute),
		conflictingEvent.ID,
	)
	if !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusRunning || stored.Version != running.Version {
		t.Fatalf("expired task failure was not rolled back: %+v", stored)
	}
}

func TestMemoryTaskTransitionStoreUpdatesTaskAndAppendsEvent(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	running, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	completedAt := task.CreatedAt.Add(2 * time.Second)
	if err := running.MoveTo(domain.TaskStatusSucceeded, completedAt); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	event := newTestEvent(t, "event_succeeded", task.ID, domain.EventTaskSucceeded, 100)

	if err := transitionStore.UpdateTaskWithEvent(ctx, running, event); err != nil {
		t.Fatalf("UpdateTaskWithEvent returned error: %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusSucceeded || stored.Version != running.Version+1 {
		t.Fatalf("unexpected stored task: %+v", stored)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("unexpected stored events: %+v", events)
	}
}

func TestMemoryTaskTransitionStoreRollsBackTaskWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	running, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	event := newTestEvent(t, "event_conflict", task.ID, domain.EventTaskSucceeded, 100)
	if err := eventStore.Append(ctx, event); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}
	if err := running.MoveTo(domain.TaskStatusSucceeded, task.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}

	if err := transitionStore.UpdateTaskWithEvent(ctx, running, event); !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusRunning || stored.Version != running.Version {
		t.Fatalf("task update was not rolled back: %+v", stored)
	}
}
