package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	platformdb "github.com/Zhonghe-zhao/taskpulse/internal/platform/database"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestMySQLTaskCancellationStoreCancelsTaskAndEventAtomicallyIntegration(t *testing.T) {
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
	creationStore, err := NewTaskCreationStore(db)
	if err != nil {
		t.Fatalf("NewTaskCreationStore returned error: %v", err)
	}
	cancellationStore, err := NewTaskTransitionStore(db)
	if err != nil {
		t.Fatalf("NewTaskTransitionStore returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_cancel_%d", suffix)
	rollbackTaskID := fmt.Sprintf("task_cancel_rollback_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(),
			"DELETE FROM tasks WHERE id IN (?, ?)",
			taskID,
			rollbackTaskID,
		)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	task, createdEvent := newMySQLTaskCreationPair(
		t,
		taskID,
		fmt.Sprintf("event_cancel_created_%d", suffix),
		now,
	)
	if _, err := creationStore.CreateTaskWithEvent(ctx, task, createdEvent); err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}
	canceledEventID := fmt.Sprintf("event_canceled_%d", suffix)
	result, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		taskID,
		canceledEventID,
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("CancelTaskWithEvent returned error: %v", err)
	}
	if !result.Canceled ||
		result.Task.Status != domain.TaskStatusCanceled ||
		result.Task.Version != 1 {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}
	replayed, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		taskID,
		fmt.Sprintf("event_canceled_replay_%d", suffix),
		now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("replayed CancelTaskWithEvent returned error: %v", err)
	}
	if replayed.Canceled || replayed.Task.Version != result.Task.Version {
		t.Fatalf("unexpected cancellation replay: %+v", replayed)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 2 ||
		events[0].Type != domain.EventTaskCreated ||
		events[1].ID != canceledEventID ||
		events[1].Type != domain.EventTaskCanceled {
		t.Fatalf("unexpected cancellation events: %+v", events)
	}

	rollbackTask, rollbackCreatedEvent := newMySQLTaskCreationPair(
		t,
		rollbackTaskID,
		fmt.Sprintf("event_cancel_rollback_created_%d", suffix),
		now,
	)
	if _, err := creationStore.CreateTaskWithEvent(
		ctx,
		rollbackTask,
		rollbackCreatedEvent,
	); err != nil {
		t.Fatalf("rollback task creation returned error: %v", err)
	}
	conflictingEventID := fmt.Sprintf("event_cancel_conflict_%d", suffix)
	conflictingEvent, err := domain.NewTaskEvent(
		conflictingEventID,
		rollbackTaskID,
		domain.EventTaskProgress,
		"seed event conflict",
		json.RawMessage(`{"seed":true}`),
		0,
		now,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if _, err := cancellationStore.CancelTaskWithEvent(
		ctx,
		rollbackTaskID,
		conflictingEventID,
		now.Add(time.Second),
	); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stored, err := taskStore.Get(ctx, rollbackTaskID)
	if err != nil {
		t.Fatalf("Get rollback task returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusQueued || stored.Version != 0 {
		t.Fatalf("task cancellation was not rolled back: %+v", stored)
	}
}

func TestMySQLTaskCancellationStoreSerializesConcurrentRequestsIntegration(t *testing.T) {
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

	creationStore, err := NewTaskCreationStore(db)
	if err != nil {
		t.Fatalf("NewTaskCreationStore returned error: %v", err)
	}
	cancellationStore, err := NewTaskTransitionStore(db)
	if err != nil {
		t.Fatalf("NewTaskTransitionStore returned error: %v", err)
	}
	eventStore, err := NewEventStore(db)
	if err != nil {
		t.Fatalf("NewEventStore returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_cancel_concurrent_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})
	now := time.Now().UTC().Truncate(time.Microsecond)
	task, createdEvent := newMySQLTaskCreationPair(
		t,
		taskID,
		fmt.Sprintf("event_cancel_concurrent_created_%d", suffix),
		now,
	)
	if _, err := creationStore.CreateTaskWithEvent(ctx, task, createdEvent); err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}

	type outcome struct {
		result *storeerrors.TaskCancellationResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for index := 0; index < 2; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			result, cancelErr := cancellationStore.CancelTaskWithEvent(
				ctx,
				taskID,
				fmt.Sprintf("event_cancel_concurrent_%d_%d", suffix, index),
				now.Add(time.Second),
			)
			outcomes <- outcome{result: result, err: cancelErr}
		}(index)
	}
	close(start)
	waitGroup.Wait()
	close(outcomes)

	canceledCount := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("CancelTaskWithEvent returned error: %v", outcome.err)
		}
		if outcome.result.Canceled {
			canceledCount++
		}
		if outcome.result.Task.Status != domain.TaskStatusCanceled {
			t.Fatalf("unexpected concurrent cancellation result: %+v", outcome.result)
		}
	}
	if canceledCount != 1 {
		t.Fatalf("expected one state transition, got %d", canceledCount)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 2 || events[1].Type != domain.EventTaskCanceled {
		t.Fatalf("unexpected events after concurrent cancellation: %+v", events)
	}
}
