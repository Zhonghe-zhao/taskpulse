package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

func TestTaskServiceCreateTask(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()

	result, err := service.CreateTask(ctx, CreateTaskInput{
		Workflow:   "url_check",
		Input:      json.RawMessage(`{"urls":["https://example.com"]}`),
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	task := result.Task
	if !result.Created {
		t.Fatal("expected first request to create a task")
	}
	if task.ID == "" {
		t.Fatal("expected generated task id")
	}
	if task.Status != domain.TaskStatusQueued {
		t.Fatalf("expected status %s, got %s", domain.TaskStatusQueued, task.Status)
	}

	got, err := service.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask returned error: %v", err)
	}
	if got.Workflow != "url_check" {
		t.Fatalf("expected workflow url_check, got %s", got.Workflow)
	}

	events, err := service.ListTaskEvents(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTaskEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != domain.EventTaskCreated {
		t.Fatalf("expected event type %s, got %s", domain.EventTaskCreated, events[0].Type)
	}
}

func TestTaskServiceListsTasksWithOpaqueCursor(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	for range 3 {
		if _, err := service.CreateTask(ctx, CreateTaskInput{Workflow: "llm_analysis"}); err != nil {
			t.Fatalf("CreateTask returned error: %v", err)
		}
	}

	first, err := service.ListTasks(ctx, ListTasksInput{Workflow: "llm_analysis", Limit: 2})
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second, err := service.ListTasks(ctx, ListTasksInput{
		Workflow: "llm_analysis",
		Cursor:   first.NextCursor,
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("ListTasks second page returned error: %v", err)
	}
	if len(second.Items) != 1 || second.HasMore {
		t.Fatalf("unexpected second page: %+v", second)
	}
}

func TestTaskServiceDetailDerivesFailureDiagnosticsWithoutLeaseToken(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	creationStore := store.NewMemoryTaskCreationStore(taskStore, eventStore)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	service := NewTaskService(taskStore, eventStore, creationStore, transitionStore)
	created, err := service.CreateTask(ctx, CreateTaskInput{Workflow: "memobridge.semantic_profile"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	event, err := domain.NewTaskEvent(
		"event_failed",
		created.Task.ID,
		domain.EventTaskFailed,
		"invalid output",
		json.RawMessage(`{"error_code":"invalid_model_output","retryable":false}`),
		0,
		created.Task.CreatedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(ctx, event); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	detail, err := service.GetTaskDetail(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("GetTaskDetail returned error: %v", err)
	}
	if detail.ErrorCode != "invalid_model_output" || detail.Retryable == nil || *detail.Retryable {
		t.Fatalf("unexpected failure diagnostics: %+v", detail)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if strings.Contains(string(encoded), "lease_token") {
		t.Fatalf("task detail leaked lease token: %s", encoded)
	}
}

func TestTaskServiceReplaysIdempotentCreation(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	first, err := service.CreateTask(ctx, CreateTaskInput{
		IdempotencyKey: "memobridge-analysis-1",
		Workflow:       "llm_analysis",
		Input:          json.RawMessage(`{"subject":"go","notes":[1,2]}`),
		MaxRetries:     3,
	})
	if err != nil {
		t.Fatalf("first CreateTask returned error: %v", err)
	}
	replayed, err := service.CreateTask(ctx, CreateTaskInput{
		IdempotencyKey: "memobridge-analysis-1",
		Workflow:       "llm_analysis",
		Input:          json.RawMessage(`{"notes":[1,2],"subject":"go"}`),
		MaxRetries:     3,
	})
	if err != nil {
		t.Fatalf("replayed CreateTask returned error: %v", err)
	}
	if !first.Created || replayed.Created {
		t.Fatalf("unexpected creation flags: first=%t replayed=%t", first.Created, replayed.Created)
	}
	if first.Task.ID != replayed.Task.ID {
		t.Fatalf("expected replayed task %s, got %s", first.Task.ID, replayed.Task.ID)
	}
	events, err := service.ListTaskEvents(ctx, first.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one created event after replay, got %d", len(events))
	}
}

func TestTaskServiceCreatesDistinctTasksWithoutIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	input := CreateTaskInput{
		Workflow:   "llm_analysis",
		Input:      json.RawMessage(`{"subject":"go"}`),
		MaxRetries: 3,
	}

	first, err := service.CreateTask(ctx, input)
	if err != nil {
		t.Fatalf("first CreateTask returned error: %v", err)
	}
	second, err := service.CreateTask(ctx, input)
	if err != nil {
		t.Fatalf("second CreateTask returned error: %v", err)
	}
	if !first.Created || !second.Created {
		t.Fatalf("expected both requests to create tasks: first=%t second=%t", first.Created, second.Created)
	}
	if first.Task.ID == second.Task.ID {
		t.Fatalf("expected distinct task IDs without idempotency key, got %s", first.Task.ID)
	}
}

func TestTaskServiceTreatsIdempotencyKeysAsCaseSensitive(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	input := CreateTaskInput{
		IdempotencyKey: "request-a",
		Workflow:       "llm_analysis",
		Input:          json.RawMessage(`{"subject":"go"}`),
		MaxRetries:     3,
	}
	first, err := service.CreateTask(ctx, input)
	if err != nil {
		t.Fatalf("first CreateTask returned error: %v", err)
	}

	input.IdempotencyKey = "REQUEST-A"
	second, err := service.CreateTask(ctx, input)
	if err != nil {
		t.Fatalf("second CreateTask returned error: %v", err)
	}
	if !first.Created || !second.Created || first.Task.ID == second.Task.ID {
		t.Fatalf("expected case-distinct keys to create different tasks: first=%+v second=%+v", first, second)
	}
}

func TestTaskServiceRejectsIdempotencyKeyReuseWithDifferentInput(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	if _, err := service.CreateTask(ctx, CreateTaskInput{
		IdempotencyKey: "memobridge-analysis-1",
		Workflow:       "llm_analysis",
		Input:          json.RawMessage(`{"subject":"go"}`),
		MaxRetries:     3,
	}); err != nil {
		t.Fatalf("first CreateTask returned error: %v", err)
	}

	_, err := service.CreateTask(ctx, CreateTaskInput{
		IdempotencyKey: "memobridge-analysis-1",
		Workflow:       "llm_analysis",
		Input:          json.RawMessage(`{"subject":"database"}`),
		MaxRetries:     3,
	})
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
}

func TestTaskServiceRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()

	testCases := []struct {
		name  string
		input CreateTaskInput
	}{
		{
			name: "empty workflow",
			input: CreateTaskInput{
				Input: json.RawMessage(`{}`),
			},
		},
		{
			name: "idempotency key with surrounding whitespace",
			input: CreateTaskInput{
				IdempotencyKey: " request-1 ",
				Workflow:       "llm_analysis",
				Input:          json.RawMessage(`{}`),
			},
		},
		{
			name: "idempotency key exceeds byte limit",
			input: CreateTaskInput{
				IdempotencyKey: string(make([]byte, 129)),
				Workflow:       "llm_analysis",
				Input:          json.RawMessage(`{}`),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := service.CreateTask(ctx, testCase.input)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func TestTaskServiceListTaskEventsRequiresExistingTask(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()

	_, err := service.ListTaskEvents(ctx, "missing_task")
	if !errors.Is(err, store.ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestTaskServiceCancelsQueuedTaskIdempotently(t *testing.T) {
	ctx := context.Background()
	service := newMemoryTaskService()
	created, err := service.CreateTask(ctx, CreateTaskInput{
		Workflow: "llm_analysis",
		Input:    json.RawMessage(`{"subject":"go"}`),
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	canceled, err := service.CancelTask(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("CancelTask returned error: %v", err)
	}
	if canceled.Status != domain.TaskStatusCanceled || canceled.FinishedAt == nil {
		t.Fatalf("unexpected canceled task: %+v", canceled)
	}
	replayed, err := service.CancelTask(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("replayed CancelTask returned error: %v", err)
	}
	if replayed.ID != canceled.ID || replayed.Version != canceled.Version {
		t.Fatalf("unexpected cancellation replay: %+v", replayed)
	}

	events, err := service.ListTaskEvents(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskEvents returned error: %v", err)
	}
	if len(events) != 2 || events[1].Type != domain.EventTaskCanceled {
		t.Fatalf("unexpected events after repeated cancellation: %+v", events)
	}
}

func TestTaskServiceCancelsRunningTask(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	taskCreationStore := store.NewMemoryTaskCreationStore(taskStore, eventStore)
	taskTransitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	service := NewTaskService(taskStore, eventStore, taskCreationStore, taskTransitionStore)
	created, err := service.CreateTask(ctx, CreateTaskInput{
		Workflow: "llm_analysis",
		Input:    json.RawMessage(`{"subject":"go"}`),
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if _, err := taskTransitionStore.ClaimNextWithEvent(
		ctx,
		store.ClaimOptions{
			WorkerID:      "worker_1",
			Now:           created.Task.CreatedAt.Add(time.Second),
			LeaseDuration: time.Minute,
		},
		"event_started",
	); err != nil {
		t.Fatalf("ClaimNextWithEvent returned error: %v", err)
	}

	canceled, err := service.CancelTask(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("CancelTask returned error: %v", err)
	}
	if canceled.Status != domain.TaskStatusCanceled {
		t.Fatalf("expected canceled task, got %+v", canceled)
	}
}

func newMemoryTaskService() *TaskService {
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	taskCreationStore := store.NewMemoryTaskCreationStore(taskStore, eventStore)
	taskTransitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	return NewTaskService(taskStore, eventStore, taskCreationStore, taskTransitionStore)
}
