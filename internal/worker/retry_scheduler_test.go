package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestRetrySchedulerPersistsRetryingTaskAndEvent(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := createRetrySchedulerTask(t, taskStore)
	calculator, err := NewBackoffCalculator(minimumJitter{})
	if err != nil {
		t.Fatalf("NewBackoffCalculator returned error: %v", err)
	}
	scheduler, err := NewRetryScheduler(transitionStore, calculator)
	if err != nil {
		t.Fatalf("NewRetryScheduler returned error: %v", err)
	}
	executionError, err := NewExecutionError(
		ErrorTransient,
		"rate_limited",
		0,
		errors.New("provider returned 429"),
	)
	if err != nil {
		t.Fatalf("NewExecutionError returned error: %v", err)
	}
	now := task.UpdatedAt.Add(time.Second)
	policy := RetryPolicy{
		MaxRetries: 3,
		BaseDelay:  2 * time.Second,
		MaxDelay:   8 * time.Second,
	}

	if err := scheduler.Schedule(ctx, task, executionError, policy, now); err != nil {
		t.Fatalf("Schedule returned error: %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusRetrying ||
		stored.RetryCount != 1 ||
		stored.Version != task.Version+1 ||
		!stored.AvailableAt.Equal(now.Add(time.Second)) {
		t.Fatalf("unexpected retrying task: %+v", stored)
	}
	if stored.ErrorMessage != "rate_limited" ||
		stored.LeaseOwner != "" ||
		stored.LeaseExpiresAt != nil {
		t.Fatalf("unexpected retrying task metadata: %+v", stored)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.EventTaskRetrying {
		t.Fatalf("unexpected retry events: %+v", events)
	}
}

func TestRetrySchedulerRejectsPermanentError(t *testing.T) {
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createRetrySchedulerTask(t, taskStore)
	calculator, err := NewBackoffCalculator(minimumJitter{})
	if err != nil {
		t.Fatalf("NewBackoffCalculator returned error: %v", err)
	}
	scheduler, err := NewRetryScheduler(
		store.NewMemoryTaskTransitionStore(taskStore, eventStore),
		calculator,
	)
	if err != nil {
		t.Fatalf("NewRetryScheduler returned error: %v", err)
	}
	executionError, err := NewExecutionError(
		ErrorPermanent,
		"invalid_input",
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("NewExecutionError returned error: %v", err)
	}

	err = scheduler.Schedule(
		context.Background(),
		task,
		executionError,
		RetryPolicy{MaxRetries: 3, BaseDelay: time.Second, MaxDelay: time.Minute},
		task.UpdatedAt.Add(time.Second),
	)
	if !errors.Is(err, ErrExecutionNotRetryable) {
		t.Fatalf("expected ErrExecutionNotRetryable, got %v", err)
	}
	stored, getErr := taskStore.Get(context.Background(), task.ID)
	if getErr != nil {
		t.Fatalf("Get returned error: %v", getErr)
	}
	if stored.Status != domain.TaskStatusRunning {
		t.Fatalf("rejected retry changed stored task: %+v", stored)
	}
}

func TestRetrySchedulerRollsBackWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := createRetrySchedulerTask(t, taskStore)
	conflictingEvent, err := domain.NewTaskEvent(
		"event_conflict",
		task.ID,
		domain.EventTaskProgress,
		"seed conflict",
		nil,
		task.Progress,
		task.UpdatedAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	calculator, err := NewBackoffCalculator(minimumJitter{})
	if err != nil {
		t.Fatalf("NewBackoffCalculator returned error: %v", err)
	}
	scheduler, err := NewRetryScheduler(transitionStore, calculator)
	if err != nil {
		t.Fatalf("NewRetryScheduler returned error: %v", err)
	}
	scheduler.newEventID = func() string { return conflictingEvent.ID }
	executionError, err := NewExecutionError(ErrorTransient, "network_timeout", 0, nil)
	if err != nil {
		t.Fatalf("NewExecutionError returned error: %v", err)
	}

	err = scheduler.Schedule(
		ctx,
		task,
		executionError,
		RetryPolicy{MaxRetries: 3, BaseDelay: time.Second, MaxDelay: time.Minute},
		task.UpdatedAt.Add(time.Second),
	)
	if !errors.Is(err, store.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, getErr := taskStore.Get(ctx, task.ID)
	if getErr != nil {
		t.Fatalf("Get returned error: %v", getErr)
	}
	if stored.Status != domain.TaskStatusRunning ||
		stored.RetryCount != 0 ||
		stored.Version != task.Version {
		t.Fatalf("retry transition was not rolled back: %+v", stored)
	}
}

func createRetrySchedulerTask(t *testing.T, taskStore store.TaskStore) *domain.Task {
	t.Helper()
	ctx := context.Background()
	createdAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	task, err := domain.NewTask("task_retry", "llm_analysis", nil, 3, createdAt)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	running, err := taskStore.ClaimNext(ctx, store.ClaimOptions{
		WorkerID:      "worker_1",
		Now:           createdAt.Add(time.Second),
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	return running
}
