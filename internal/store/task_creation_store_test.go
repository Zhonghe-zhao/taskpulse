package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

func TestMemoryTaskCreationStoreCreatesTaskAndEvent(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	creator := NewMemoryTaskCreationStore(taskStore, eventStore)
	task, event := newTaskCreationPair(t, "task_1", "event_1")

	result, err := creator.CreateTaskWithEvent(ctx, task, event)
	if err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}
	if !result.Created || result.Task.ID != task.ID {
		t.Fatalf("unexpected creation result: %+v", result)
	}
	if _, err := taskStore.Get(ctx, task.ID); err != nil {
		t.Fatalf("Get task returned error: %v", err)
	}
	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestMemoryTaskCreationStoreDoesNotCreateTaskWhenEventConflicts(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	creator := NewMemoryTaskCreationStore(taskStore, eventStore)
	task, event := newTaskCreationPair(t, "task_1", "event_1")

	if err := eventStore.Append(ctx, event); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}
	if _, err := creator.CreateTaskWithEvent(ctx, task, event); !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	if _, err := taskStore.Get(ctx, task.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("task was created despite event conflict: %v", err)
	}
}

func TestMemoryTaskCreationStoreReplaysSameIdempotentRequest(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	creator := NewMemoryTaskCreationStore(taskStore, eventStore)
	firstTask, firstEvent := newTaskCreationPair(t, "task_1", "event_1")
	firstTask.IdempotencyKey = "memobridge-analysis-1"
	if _, err := creator.CreateTaskWithEvent(ctx, firstTask, firstEvent); err != nil {
		t.Fatalf("first CreateTaskWithEvent returned error: %v", err)
	}
	replayTask, replayEvent := newTaskCreationPair(t, "task_2", "event_2")
	replayTask.IdempotencyKey = firstTask.IdempotencyKey
	replayTask.Input = json.RawMessage(`{"urls": [ "https://example.com" ]}`)

	result, err := creator.CreateTaskWithEvent(ctx, replayTask, replayEvent)
	if err != nil {
		t.Fatalf("replayed CreateTaskWithEvent returned error: %v", err)
	}
	if result.Created || result.Task.ID != firstTask.ID {
		t.Fatalf("unexpected replay result: %+v", result)
	}
	if _, err := taskStore.Get(ctx, replayTask.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("replay task should not have been inserted: %v", err)
	}
	events, err := eventStore.ListByTaskID(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one created event, got %d", len(events))
	}
}

func TestMemoryTaskCreationStoreAllowsSameIdempotencyKeyAcrossWorkflows(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	creator := NewMemoryTaskCreationStore(taskStore, eventStore)
	firstTask, firstEvent := newTaskCreationPair(t, "task_1", "event_1")
	firstTask.IdempotencyKey = "memobridge-analysis-1"
	if _, err := creator.CreateTaskWithEvent(ctx, firstTask, firstEvent); err != nil {
		t.Fatalf("first CreateTaskWithEvent returned error: %v", err)
	}
	conflictTask, conflictEvent := newTaskCreationPair(t, "task_2", "event_2")
	conflictTask.IdempotencyKey = firstTask.IdempotencyKey
	conflictTask.Workflow = "different_workflow"

	result, err := creator.CreateTaskWithEvent(ctx, conflictTask, conflictEvent)
	if err != nil {
		t.Fatalf("expected different workflow to allow the same idempotency key, got %v", err)
	}
	if !result.Created || result.Task.ID != conflictTask.ID {
		t.Fatalf("expected a new task for the different workflow, got %+v", result)
	}
}

func TestMemoryTaskCreationStoreCreatesIdempotentTaskOnceConcurrently(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	eventStore := NewMemoryEventStore()
	creator := NewMemoryTaskCreationStore(taskStore, eventStore)

	const requests = 8
	tasks := make([]*domain.Task, requests)
	events := make([]*domain.TaskEvent, requests)
	for index := 0; index < requests; index++ {
		tasks[index], events[index] = newTaskCreationPair(
			t,
			fmt.Sprintf("task_%d", index),
			fmt.Sprintf("event_%d", index),
		)
		tasks[index].IdempotencyKey = "memobridge-analysis-concurrent"
	}
	type outcome struct {
		result *TaskCreationResult
		err    error
	}
	outcomes := make(chan outcome, requests)
	var waitGroup sync.WaitGroup
	for index := 0; index < requests; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			result, err := creator.CreateTaskWithEvent(ctx, tasks[index], events[index])
			outcomes <- outcome{result: result, err: err}
		}(index)
	}
	waitGroup.Wait()
	close(outcomes)

	created := 0
	var taskID string
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("CreateTaskWithEvent returned error: %v", outcome.err)
		}
		if outcome.result.Created {
			created++
		}
		if taskID == "" {
			taskID = outcome.result.Task.ID
		} else if outcome.result.Task.ID != taskID {
			t.Fatalf("expected every request to return task %s, got %s", taskID, outcome.result.Task.ID)
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly one creation, got %d", created)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one created event, got %d", len(events))
	}
}

func newTaskCreationPair(t *testing.T, taskID, eventID string) (*domain.Task, *domain.TaskEvent) {
	t.Helper()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(
		taskID,
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		3,
		now,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	event, err := domain.NewTaskEvent(
		eventID,
		task.ID,
		domain.EventTaskCreated,
		"task created",
		nil,
		0,
		now,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	return task, event
}
