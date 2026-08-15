package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
)

func TestMemoryTaskCancellationStoreCancelsQueuedTaskIdempotently(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	cancellationStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	canceledAt := task.CreatedAt.Add(time.Second)
	result, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		"event_canceled",
		canceledAt,
	)
	if err != nil {
		t.Fatalf("CancelTaskWithEvent returned error: %v", err)
	}
	if !result.Canceled ||
		result.Task.Status != domain.TaskStatusCanceled ||
		result.Task.Version != 1 ||
		result.Task.FinishedAt == nil ||
		!result.Task.FinishedAt.Equal(canceledAt) {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}

	replayed, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		"event_canceled_replay",
		canceledAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("replayed CancelTaskWithEvent returned error: %v", err)
	}
	if replayed.Canceled || replayed.Task.Version != result.Task.Version {
		t.Fatalf("unexpected replay result: %+v", replayed)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.EventTaskCanceled {
		t.Fatalf("unexpected cancellation events: %+v", events)
	}
}

func TestMemoryTaskCancellationStoreCancelsRetryingTask(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	runningAt := task.CreatedAt.Add(time.Second)
	running, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           runningAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	retryingAt := runningAt.Add(time.Second)
	availableAt := retryingAt.Add(time.Minute)
	if err := running.ScheduleRetry(retryingAt, availableAt); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}
	retryingEvent, err := domain.NewTaskRetryingEvent(
		"event_retrying",
		running,
		"temporary_failure",
		availableAt.Sub(retryingAt),
		retryingAt,
	)
	if err != nil {
		t.Fatalf("NewTaskRetryingEvent returned error: %v", err)
	}
	if err := transitionStore.UpdateTaskWithEvent(ctx, running, retryingEvent); err != nil {
		t.Fatalf("UpdateTaskWithEvent returned error: %v", err)
	}

	result, err := transitionStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		"event_canceled",
		retryingAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("CancelTaskWithEvent returned error: %v", err)
	}
	if !result.Canceled || result.Task.Status != domain.TaskStatusCanceled {
		t.Fatalf("unexpected retrying cancellation result: %+v", result)
	}
}

func TestMemoryTaskCancellationStoreCancelsRunningTask(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	cancellationStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	result, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		"event_canceled",
		task.CreatedAt.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("CancelTaskWithEvent returned error: %v", err)
	}
	if !result.Canceled || result.Task.Status != domain.TaskStatusCanceled {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}
}

func TestMemoryTaskCancellationStoreRollsBackWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	cancellationStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	conflictingEvent := newTestEvent(
		t,
		"event_conflict",
		task.ID,
		domain.EventTaskProgress,
		0,
	)
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	if _, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		conflictingEvent.ID,
		task.CreatedAt.Add(time.Second),
	); !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusQueued || stored.Version != 0 {
		t.Fatalf("task cancellation was not rolled back: %+v", stored)
	}
}

func TestMemoryTaskCancellationWinsAgainstConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	transitionStore := NewMemoryTaskTransitionStore(taskStore, eventStore)
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	now := task.CreatedAt.Add(time.Second)

	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	results := make(chan error, 2)
	go func() {
		defer waitGroup.Done()
		<-start
		_, err := transitionStore.ClaimNextWithEvent(
			ctx,
			ClaimOptions{
				WorkerID:      "worker_1",
				Now:           now,
				LeaseDuration: time.Minute,
			},
			"event_started",
		)
		results <- err
	}()
	go func() {
		defer waitGroup.Done()
		<-start
		_, err := transitionStore.CancelTaskWithEvent(
			ctx,
			task.ID,
			"event_canceled",
			now,
		)
		results <- err
	}()
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrNoTaskAvailable) && !errors.Is(err, ErrTaskNotCancelable) {
			t.Fatalf("unexpected concurrent transition error: %v", err)
		}
	}
	if successes < 1 || successes > 2 {
		t.Fatalf("expected cancellation alone or claim followed by cancellation, got %d successes", successes)
	}
	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusCanceled {
		t.Fatalf("cancellation must win the concurrent transition, got %+v", stored)
	}
}
