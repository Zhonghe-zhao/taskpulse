package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

type fakeExecutor struct {
	result ExecutionResult
	err    error
}

type delayedExecutor struct {
	delay  time.Duration
	result ExecutionResult
}

type cancellationAwareExecutor struct {
	started chan<- struct{}
}

type retryOnceExecutor struct {
	calls      atomic.Int32
	firstErr   error
	thenResult ExecutionResult
}

func (e *retryOnceExecutor) Execute(context.Context, *domain.Task) (ExecutionResult, error) {
	if e.calls.Add(1) == 1 {
		return ExecutionResult{}, e.firstErr
	}
	return e.thenResult, nil
}

func (e delayedExecutor) Execute(ctx context.Context, _ *domain.Task) (ExecutionResult, error) {
	timer := time.NewTimer(e.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ExecutionResult{}, ctx.Err()
	case <-timer.C:
		return e.result, nil
	}
}

func (e cancellationAwareExecutor) Execute(ctx context.Context, _ *domain.Task) (ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	return ExecutionResult{}, ctx.Err()
}

type countingTaskStore struct {
	store.TaskStore
	renewals atomic.Int32
}

type lostLeaseTaskStore struct {
	store.TaskStore
}

type cancelDuringRenewTaskStore struct {
	store.TaskStore
}

func (s *lostLeaseTaskStore) RenewLease(context.Context, store.RenewLeaseOptions) error {
	return store.ErrLeaseLost
}

func (s *cancelDuringRenewTaskStore) RenewLease(ctx context.Context, _ store.RenewLeaseOptions) error {
	<-ctx.Done()
	return ctx.Err()
}

func (s *countingTaskStore) RenewLease(ctx context.Context, options store.RenewLeaseOptions) error {
	s.renewals.Add(1)
	return s.TaskStore.RenewLease(ctx, options)
}

func (e fakeExecutor) Execute(context.Context, *domain.Task) (ExecutionResult, error) {
	return e.result, e.err
}

func TestWorkerCompletesTask(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": fakeExecutor{result: ExecutionResult{
			Output:  json.RawMessage(`{"checked":1}`),
			Outcome: OutcomeSucceeded,
		}},
	}, nil)
	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected one task to be processed")
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != domain.TaskStatusSucceeded || got.Progress != 100 {
		t.Fatalf("unexpected completed task: %+v", got)
	}
	if got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("expected completed task lease to be cleared: %+v", got)
	}

	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[1].Type != domain.EventTaskStarted || events[2].Type != domain.EventTaskSucceeded {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
}

func TestWorkerPreservesPartialResult(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": fakeExecutor{result: ExecutionResult{
			Output:       json.RawMessage(`{"succeeded":1,"failed":1}`),
			Outcome:      OutcomePartial,
			ErrorMessage: "1 of 2 URL checks failed",
		}},
	}, nil)
	if _, err := w.ProcessNext(ctx); err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != domain.TaskStatusPartial {
		t.Fatalf("expected partially_succeeded, got %s", got.Status)
	}
	if len(got.Result) == 0 || got.ErrorMessage == "" {
		t.Fatalf("expected partial output and error summary, got %+v", got)
	}
}

func TestWorkerMarksExecutionFailure(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": fakeExecutor{err: errors.New("request timed out")},
	}, nil)
	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected one task to be processed")
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != domain.TaskStatusFailed || got.ErrorMessage != "request timed out" {
		t.Fatalf("unexpected failed task: %+v", got)
	}
}

func TestWorkerRetriesTransientFailureAndThenSucceeds(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTaskForWorkflow(t, taskStore, eventStore, "llm_analysis")
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	executionError, err := NewExecutionError(
		ErrorTransient,
		"rate_limited",
		0,
		errors.New("provider returned 429"),
	)
	if err != nil {
		t.Fatalf("NewExecutionError returned error: %v", err)
	}
	executor := &retryOnceExecutor{
		firstErr: executionError,
		thenResult: ExecutionResult{
			Output:  json.RawMessage(`{"answer":"ok"}`),
			Outcome: OutcomeSucceeded,
		},
	}
	w := New(
		taskStore,
		transitionStore,
		map[string]Executor{"llm_analysis": executor},
		map[string]RetryPolicy{
			"llm_analysis": {
				MaxRetries: 3,
				BaseDelay:  2 * time.Second,
				MaxDelay:   8 * time.Second,
			},
		},
	)
	calculator, err := NewBackoffCalculator(minimumJitter{})
	if err != nil {
		t.Fatalf("NewBackoffCalculator returned error: %v", err)
	}
	w.retryScheduler, err = NewRetryScheduler(transitionStore, calculator)
	if err != nil {
		t.Fatalf("NewRetryScheduler returned error: %v", err)
	}

	firstAttemptAt := task.CreatedAt.Add(time.Second)
	w.now = func() time.Time { return firstAttemptAt }
	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("first ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected first attempt to be processed")
	}
	retrying, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get retrying task returned error: %v", err)
	}
	expectedRetryAt := firstAttemptAt.Add(time.Second)
	if retrying.Status != domain.TaskStatusRetrying ||
		retrying.RetryCount != 1 ||
		!retrying.AvailableAt.Equal(expectedRetryAt) {
		t.Fatalf("unexpected retrying task: %+v", retrying)
	}

	w.now = func() time.Time { return expectedRetryAt }
	processed, err = w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("second ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected retry attempt to be processed")
	}
	succeeded, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get succeeded task returned error: %v", err)
	}
	if succeeded.Status != domain.TaskStatusSucceeded ||
		succeeded.RetryCount != 1 ||
		executor.calls.Load() != 2 {
		t.Fatalf("unexpected succeeded task or call count: task=%+v calls=%d", succeeded, executor.calls.Load())
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	wantTypes := []domain.EventType{
		domain.EventTaskCreated,
		domain.EventTaskStarted,
		domain.EventTaskRetrying,
		domain.EventTaskRetryStarted,
		domain.EventTaskSucceeded,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("expected %d events, got %d: %+v", len(wantTypes), len(events), events)
	}
	for index, wantType := range wantTypes {
		if events[index].Type != wantType {
			t.Fatalf("event %d: expected %s, got %s", index, wantType, events[index].Type)
		}
	}
}

func TestWorkerFailsTransientErrorWhenRetryBudgetIsExhausted(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTaskForWorkflow(t, taskStore, eventStore, "llm_analysis")
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	executionError, err := NewExecutionError(
		ErrorTransient,
		"rate_limited",
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("NewExecutionError returned error: %v", err)
	}
	w := New(
		taskStore,
		transitionStore,
		map[string]Executor{
			"llm_analysis": fakeExecutor{err: executionError},
		},
		map[string]RetryPolicy{
			"llm_analysis": {
				MaxRetries: 0,
				BaseDelay:  time.Second,
				MaxDelay:   time.Minute,
			},
		},
	)
	w.now = func() time.Time { return task.CreatedAt.Add(time.Second) }

	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected task to be processed")
	}
	failed, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if failed.Status != domain.TaskStatusFailed ||
		failed.RetryCount != 0 ||
		failed.ErrorMessage != "rate_limited" {
		t.Fatalf("unexpected failed task: %+v", failed)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 || events[2].Type != domain.EventTaskFailed {
		t.Fatalf("unexpected exhausted retry events: %+v", events)
	}
}

func TestWorkerReturnsNoWork(t *testing.T) {
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	w := New(taskStore, transitionStore, nil, nil)
	processed, err := w.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if processed {
		t.Fatal("expected no task to be processed")
	}
}

func TestWorkerRenewsLeaseDuringLongExecution(t *testing.T) {
	ctx := context.Background()
	memoryStore := store.NewMemoryTaskStore()
	taskStore := &countingTaskStore{TaskStore: memoryStore}
	eventStore := store.NewMemoryEventStore()
	createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(memoryStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": delayedExecutor{
			delay: 80 * time.Millisecond,
			result: ExecutionResult{
				Output:  json.RawMessage(`{"checked":1}`),
				Outcome: OutcomeSucceeded,
			},
		},
	}, nil)
	w.leaseDuration = 60 * time.Millisecond

	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected one task to be processed")
	}
	if taskStore.renewals.Load() == 0 {
		t.Fatal("expected at least one lease renewal")
	}
}

func TestWorkerIgnoresRenewalCanceledAfterSuccessfulExecution(t *testing.T) {
	ctx := context.Background()
	memoryStore := store.NewMemoryTaskStore()
	taskStore := &cancelDuringRenewTaskStore{TaskStore: memoryStore}
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(memoryStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": delayedExecutor{
			delay: 20 * time.Millisecond,
			result: ExecutionResult{
				Output:  json.RawMessage(`{"ok":true}`),
				Outcome: OutcomeSucceeded,
			},
		},
	}, nil)
	w.leaseDuration = 15 * time.Millisecond

	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected one task to be processed")
	}

	stored, err := memoryStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusSucceeded {
		t.Fatalf("expected task to succeed, got %s", stored.Status)
	}
}

func TestWorkerStopsExecutionWhenRunningTaskIsCanceled(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	started := make(chan struct{})

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": cancellationAwareExecutor{started: started},
	}, nil)
	w.leaseDuration = 15 * time.Millisecond

	processResult := make(chan error, 1)
	go func() {
		_, err := w.ProcessNext(ctx)
		processResult <- err
	}()
	<-started

	if _, err := transitionStore.CancelTaskWithEvent(
		ctx,
		task.ID,
		"event_canceled",
		task.CreatedAt.Add(time.Second),
	); err != nil {
		t.Fatalf("CancelTaskWithEvent returned error: %v", err)
	}

	select {
	case err := <-processResult:
		if !errors.Is(err, store.ErrLeaseLost) {
			t.Fatalf("expected ErrLeaseLost after cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop canceled task")
	}

	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusCanceled {
		t.Fatalf("expected canceled task, got %s", stored.Status)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
	foundCanceled := false
	for _, event := range events {
		if event.Type == domain.EventTaskCanceled {
			foundCanceled = true
			break
		}
	}
	if !foundCanceled {
		t.Fatalf("expected task_canceled event, got %+v", events)
	}
}

func TestWorkerStopsWritingAfterLeaseIsLost(t *testing.T) {
	ctx := context.Background()
	memoryStore := store.NewMemoryTaskStore()
	taskStore := &lostLeaseTaskStore{TaskStore: memoryStore}
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(memoryStore, eventStore)

	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": delayedExecutor{
			delay: time.Second,
			result: ExecutionResult{
				Outcome: OutcomeSucceeded,
			},
		},
	}, nil)
	w.leaseDuration = 15 * time.Millisecond

	processed, err := w.ProcessNext(ctx)
	if !processed {
		t.Fatal("expected one task to be claimed")
	}
	if !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost, got %v", err)
	}

	stored, getErr := memoryStore.Get(ctx, task.ID)
	if getErr != nil {
		t.Fatalf("Get returned error: %v", getErr)
	}
	if stored.Status != domain.TaskStatusRunning {
		t.Fatalf("worker without lease changed task status to %s", stored.Status)
	}
}

func TestWorkerEmitsRecoveredEventForExpiredTask(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	task := createTask(t, taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)

	claimedAt := task.CreatedAt.Add(time.Minute)
	if _, err := taskStore.ClaimNext(ctx, store.ClaimOptions{
		WorkerID:      "crashed_worker",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("initial ClaimNext returned error: %v", err)
	}

	recoveredAt := claimedAt.Add(time.Minute)
	w := New(taskStore, transitionStore, map[string]Executor{
		"url_check": fakeExecutor{result: ExecutionResult{
			Output:  json.RawMessage(`{"checked":1}`),
			Outcome: OutcomeSucceeded,
		}},
	}, nil)
	w.id = "recovery_worker"
	w.leaseDuration = time.Minute
	w.now = func() time.Time { return recoveredAt }

	processed, err := w.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected expired task to be recovered")
	}

	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[1].Type != domain.EventTaskRecovered {
		t.Fatalf("expected recovered event, got %s", events[1].Type)
	}
}

func createTask(t *testing.T, taskStore store.TaskStore, eventStore store.EventStore) *domain.Task {
	return createTaskForWorkflow(t, taskStore, eventStore, "url_check")
}

func createTaskForWorkflow(
	t *testing.T,
	taskStore store.TaskStore,
	eventStore store.EventStore,
	workflow string,
) *domain.Task {
	t.Helper()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(
		"task_1",
		workflow,
		json.RawMessage(`{"input":"test"}`),
		3,
		now,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := taskStore.Create(context.Background(), task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	event, err := domain.NewTaskEvent("event_created", task.ID, domain.EventTaskCreated, "task created", nil, 0, now)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(context.Background(), event); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	return task
}
