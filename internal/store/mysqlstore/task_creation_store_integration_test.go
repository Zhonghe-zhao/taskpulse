package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	platformdb "github.com/Zhonghe-zhao/taskpulse/internal/platform/database"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestMySQLTaskCreationStoreCommitsTaskAndEventIntegration(t *testing.T) {
	if os.Getenv("TASKPULSE_MYSQL_INTEGRATION") != "1" {
		t.Skip("set TASKPULSE_MYSQL_INTEGRATION=1 to run MySQL store integration tests")
	}

	config, err := platformdb.MySQLConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLConfigFromEnv returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformdb.OpenMySQL(ctx, config)
	if err != nil {
		t.Fatalf("OpenMySQL returned error: %v", err)
	}
	defer db.Close()

	taskStore, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("NewTaskStore returned error: %v", err)
	}
	eventStore, err := NewEventStore(db)
	if err != nil {
		t.Fatalf("NewEventStore returned error: %v", err)
	}
	creator, err := NewTaskCreationStore(db)
	if err != nil {
		t.Fatalf("NewTaskCreationStore returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_atomic_%d", suffix)
	eventID := fmt.Sprintf("event_atomic_%d", suffix)
	rollbackTaskID := fmt.Sprintf("task_atomic_rollback_%d", suffix)
	conflictTaskID := fmt.Sprintf("task_atomic_conflict_%d", suffix)
	caseVariantTaskID := fmt.Sprintf("task_atomic_case_variant_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(),
			"DELETE FROM tasks WHERE id IN (?, ?, ?, ?)",
			taskID,
			rollbackTaskID,
			conflictTaskID,
			caseVariantTaskID,
		)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	task, event := newMySQLTaskCreationPair(t, taskID, eventID, now)
	task.IdempotencyKey = fmt.Sprintf("idem_atomic_%d", suffix)
	result, err := creator.CreateTaskWithEvent(ctx, task, event)
	if err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}
	if !result.Created {
		t.Fatal("expected first request to create task")
	}
	if _, err := taskStore.Get(ctx, taskID); err != nil {
		t.Fatalf("Get committed task returned error: %v", err)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 1 || events[0].ID != eventID {
		t.Fatalf("unexpected committed events: %+v", events)
	}
	var outboxCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM task_outbox WHERE task_id = ?", taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count task outbox records: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("task creation wrote %d outbox records without an active dispatcher", outboxCount)
	}

	replayTask, replayEvent := newMySQLTaskCreationPair(
		t,
		fmt.Sprintf("task_atomic_replay_%d", suffix),
		fmt.Sprintf("event_atomic_replay_%d", suffix),
		now.Add(time.Second),
	)
	replayTask.IdempotencyKey = task.IdempotencyKey
	replayed, err := creator.CreateTaskWithEvent(ctx, replayTask, replayEvent)
	if err != nil {
		t.Fatalf("replayed CreateTaskWithEvent returned error: %v", err)
	}
	if replayed.Created || replayed.Task.ID != task.ID {
		t.Fatalf("unexpected replay result: %+v", replayed)
	}

	conflictTask, conflictEvent := newMySQLTaskCreationPair(
		t,
		conflictTaskID,
		fmt.Sprintf("event_atomic_conflict_%d", suffix),
		now.Add(2*time.Second),
	)
	conflictTask.IdempotencyKey = task.IdempotencyKey
	conflictTask.Workflow = "different_workflow"
	conflictResult, err := creator.CreateTaskWithEvent(ctx, conflictTask, conflictEvent)
	if err != nil {
		t.Fatalf("expected different workflow to allow the same idempotency key, got %v", err)
	}
	if !conflictResult.Created || conflictResult.Task.ID != conflictTask.ID {
		t.Fatalf("expected different workflow task to be created, got %+v", conflictResult)
	}
	events, err = eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID after replay returned error: %v", err)
	}
	if len(events) != 1 || events[0].ID != eventID {
		t.Fatalf("replay or conflict created additional events: %+v", events)
	}

	caseVariantTask, caseVariantEvent := newMySQLTaskCreationPair(
		t,
		caseVariantTaskID,
		fmt.Sprintf("event_atomic_case_variant_%d", suffix),
		now.Add(3*time.Second),
	)
	caseVariantTask.IdempotencyKey = strings.ToUpper(task.IdempotencyKey)
	caseVariantResult, err := creator.CreateTaskWithEvent(ctx, caseVariantTask, caseVariantEvent)
	if err != nil {
		t.Fatalf("case-variant CreateTaskWithEvent returned error: %v", err)
	}
	if !caseVariantResult.Created || caseVariantResult.Task.ID != caseVariantTaskID {
		t.Fatalf("case-distinct idempotency key did not create a new task: %+v", caseVariantResult)
	}

	rollbackTask, rollbackEvent := newMySQLTaskCreationPair(
		t,
		rollbackTaskID,
		eventID,
		now.Add(time.Second),
	)
	if _, err := creator.CreateTaskWithEvent(ctx, rollbackTask, rollbackEvent); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	if _, err := taskStore.Get(ctx, rollbackTaskID); !errors.Is(err, storeerrors.ErrTaskNotFound) {
		t.Fatalf("task insert was not rolled back: %v", err)
	}
}

func TestMySQLTaskCreationStoreCreatesIdempotentTaskOnceConcurrentlyIntegration(t *testing.T) {
	if os.Getenv("TASKPULSE_MYSQL_INTEGRATION") != "1" {
		t.Skip("set TASKPULSE_MYSQL_INTEGRATION=1 to run MySQL store integration tests")
	}

	config, err := platformdb.MySQLConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLConfigFromEnv returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformdb.OpenMySQL(ctx, config)
	if err != nil {
		t.Fatalf("OpenMySQL returned error: %v", err)
	}
	defer db.Close()

	creator, err := NewTaskCreationStore(db)
	if err != nil {
		t.Fatalf("NewTaskCreationStore returned error: %v", err)
	}
	eventStore, err := NewEventStore(db)
	if err != nil {
		t.Fatalf("NewEventStore returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	idempotencyKey := fmt.Sprintf("idem_concurrent_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(),
			"DELETE FROM tasks WHERE idempotency_key = ?",
			idempotencyKey,
		)
	})

	const requests = 8
	tasks := make([]*domain.Task, requests)
	events := make([]*domain.TaskEvent, requests)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for index := 0; index < requests; index++ {
		tasks[index], events[index] = newMySQLTaskCreationPair(
			t,
			fmt.Sprintf("task_idem_%d_%d", suffix, index),
			fmt.Sprintf("event_idem_%d_%d", suffix, index),
			now,
		)
		tasks[index].IdempotencyKey = idempotencyKey
	}

	type outcome struct {
		result *storeerrors.TaskCreationResult
		err    error
	}
	outcomes := make(chan outcome, requests)
	var waitGroup sync.WaitGroup
	for index := 0; index < requests; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			result, createErr := creator.CreateTaskWithEvent(ctx, tasks[index], events[index])
			outcomes <- outcome{result: result, err: createErr}
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
	storedEvents, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(storedEvents) != 1 || storedEvents[0].Type != domain.EventTaskCreated {
		t.Fatalf("unexpected events after concurrent creation: %+v", storedEvents)
	}
}

func newMySQLTaskCreationPair(
	t *testing.T,
	taskID string,
	eventID string,
	now time.Time,
) (*domain.Task, *domain.TaskEvent) {
	t.Helper()
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
