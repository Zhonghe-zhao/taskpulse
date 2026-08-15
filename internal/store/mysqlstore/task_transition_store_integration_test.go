package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	platformdb "github.com/Zhonghe-zhao/taskpulse/internal/platform/database"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestMySQLTaskTransitionStoreCommitsTaskAndEventIntegration(t *testing.T) {
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
	transitionStore, err := NewTaskTransitionStore(db)
	if err != nil {
		t.Fatalf("NewTaskTransitionStore returned error: %v", err)
	}
	if err := cleanupActiveTasks(ctx, db); err != nil {
		t.Fatalf("cleanupActiveTasks returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_transition_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	task, createdEvent := newMySQLTaskCreationPair(
		t,
		taskID,
		fmt.Sprintf("event_created_%d", suffix),
		createdAt,
	)
	if _, err := creationStore.CreateTaskWithEvent(ctx, task, createdEvent); err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}
	running, err := taskStore.ClaimNext(ctx, storeerrors.ClaimOptions{
		WorkerID:      "worker_1",
		Now:           createdAt.Add(time.Second),
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	conflictingEventID := fmt.Sprintf("event_conflict_%d", suffix)
	conflictingEvent, err := domain.NewTaskEvent(
		conflictingEventID,
		taskID,
		domain.EventTaskProgress,
		"seed event conflict",
		json.RawMessage(`{"seed":true}`),
		50,
		createdAt.Add(1500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewTaskEvent conflicting returned error: %v", err)
	}
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}

	completedAt := createdAt.Add(2 * time.Second)
	if err := running.MoveTo(domain.TaskStatusSucceeded, completedAt); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	duplicateEvent, err := domain.NewTaskEvent(
		conflictingEventID,
		taskID,
		domain.EventTaskSucceeded,
		"task succeeded",
		nil,
		100,
		completedAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent duplicate returned error: %v", err)
	}
	if err := transitionStore.UpdateTaskWithEvent(ctx, running, duplicateEvent); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stillRunning, err := taskStore.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get after rollback returned error: %v", err)
	}
	if stillRunning.Status != domain.TaskStatusRunning || stillRunning.Version != running.Version {
		t.Fatalf("task update was not rolled back: %+v", stillRunning)
	}

	succeededEvent, err := domain.NewTaskEvent(
		fmt.Sprintf("event_succeeded_%d", suffix),
		taskID,
		domain.EventTaskSucceeded,
		"task succeeded",
		nil,
		100,
		completedAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent succeeded returned error: %v", err)
	}
	if err := transitionStore.UpdateTaskWithEvent(ctx, running, succeededEvent); err != nil {
		t.Fatalf("UpdateTaskWithEvent returned error: %v", err)
	}
	succeeded, err := taskStore.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get succeeded task returned error: %v", err)
	}
	if succeeded.Status != domain.TaskStatusSucceeded || succeeded.Version != running.Version+1 {
		t.Fatalf("unexpected succeeded task: %+v", succeeded)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 || events[2].ID != succeededEvent.ID {
		t.Fatalf("unexpected events after transition: %+v", events)
	}
}

func TestMySQLTaskTransitionStoreClaimsTaskAndEventAtomicallyIntegration(t *testing.T) {
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
	transitionStore, err := NewTaskTransitionStore(db)
	if err != nil {
		t.Fatalf("NewTaskTransitionStore returned error: %v", err)
	}
	if err := cleanupActiveTasks(ctx, db); err != nil {
		t.Fatalf("cleanupActiveTasks returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_claim_transition_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	task, createdEvent := newMySQLTaskCreationPair(
		t,
		taskID,
		fmt.Sprintf("event_created_%d", suffix),
		createdAt,
	)
	if _, err := creationStore.CreateTaskWithEvent(ctx, task, createdEvent); err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}

	conflictingEventID := fmt.Sprintf("event_claim_conflict_%d", suffix)
	conflictingEvent, err := domain.NewTaskEvent(
		conflictingEventID,
		taskID,
		domain.EventTaskProgress,
		"seed event conflict",
		nil,
		0,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent conflicting returned error: %v", err)
	}
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}

	claimOptions := storeerrors.ClaimOptions{
		WorkerID:      "worker_1",
		Now:           createdAt.Add(time.Second),
		LeaseDuration: time.Minute,
	}
	if _, err := transitionStore.ClaimNextWithEvent(ctx, claimOptions, conflictingEventID); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stillQueued, err := taskStore.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get after rolled back claim returned error: %v", err)
	}
	if stillQueued.Status != domain.TaskStatusQueued || stillQueued.Version != 0 {
		t.Fatalf("task claim was not rolled back: %+v", stillQueued)
	}

	startedEventID := fmt.Sprintf("event_started_%d", suffix)
	claimed, err := transitionStore.ClaimNextWithEvent(ctx, claimOptions, startedEventID)
	if err != nil {
		t.Fatalf("ClaimNextWithEvent returned error: %v", err)
	}
	if claimed.ID != taskID || claimed.Status != domain.TaskStatusRunning || claimed.Version != 1 {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 || events[2].ID != startedEventID || events[2].Type != domain.EventTaskStarted {
		t.Fatalf("unexpected events after claim: %+v", events)
	}

	scheduledAt := claimOptions.Now.Add(time.Second)
	retryAt := scheduledAt.Add(10 * time.Second)
	if err := claimed.ScheduleRetry(scheduledAt, retryAt); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}
	retryingEvent, err := domain.NewTaskRetryingEvent(
		fmt.Sprintf("event_retrying_%d", suffix),
		claimed,
		"rate_limited",
		retryAt.Sub(scheduledAt),
		scheduledAt,
	)
	if err != nil {
		t.Fatalf("NewTaskRetryingEvent returned error: %v", err)
	}
	if err := transitionStore.UpdateTaskWithEvent(ctx, claimed, retryingEvent); err != nil {
		t.Fatalf("UpdateTaskWithEvent retrying returned error: %v", err)
	}

	earlyOptions := claimOptions
	earlyOptions.Now = retryAt.Add(-time.Microsecond)
	if _, err := transitionStore.ClaimNextWithEvent(
		ctx,
		earlyOptions,
		fmt.Sprintf("event_retry_too_early_%d", suffix),
	); !errors.Is(err, storeerrors.ErrNoTaskAvailable) {
		t.Fatalf("expected ErrNoTaskAvailable before retry time, got %v", err)
	}

	retryOptions := claimOptions
	retryOptions.Now = retryAt
	retryStartedEventID := fmt.Sprintf("event_retry_started_%d", suffix)
	retried, err := transitionStore.ClaimNextWithEvent(ctx, retryOptions, retryStartedEventID)
	if err != nil {
		t.Fatalf("ClaimNextWithEvent retry returned error: %v", err)
	}
	if retried.ID != taskID ||
		retried.Status != domain.TaskStatusRunning ||
		retried.RetryCount != 1 ||
		retried.Version != 3 {
		t.Fatalf("unexpected retried task: %+v", retried)
	}
	events, err = eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID after retry returned error: %v", err)
	}
	if len(events) != 5 ||
		events[3].Type != domain.EventTaskRetrying ||
		events[4].ID != retryStartedEventID ||
		events[4].Type != domain.EventTaskRetryStarted {
		t.Fatalf("unexpected events after retry claim: %+v", events)
	}
}

func TestMySQLTaskTransitionStoreFailsExpiredTaskAndEventAtomicallyIntegration(t *testing.T) {
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
	transitionStore, err := NewTaskTransitionStore(db)
	if err != nil {
		t.Fatalf("NewTaskTransitionStore returned error: %v", err)
	}
	if err := cleanupActiveTasks(ctx, db); err != nil {
		t.Fatalf("cleanupActiveTasks returned error: %v", err)
	}

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_expired_transition_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	task, createdEvent := newMySQLTaskCreationPair(
		t,
		taskID,
		fmt.Sprintf("event_created_%d", suffix),
		createdAt,
	)
	task.MaxRetries = 0
	if _, err := creationStore.CreateTaskWithEvent(ctx, task, createdEvent); err != nil {
		t.Fatalf("CreateTaskWithEvent returned error: %v", err)
	}

	claimedAt := createdAt.Add(time.Second)
	running, err := taskStore.ClaimNext(ctx, storeerrors.ClaimOptions{
		WorkerID:      "crashed_worker",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	conflictingEventID := fmt.Sprintf("event_expired_conflict_%d", suffix)
	conflictingEvent, err := domain.NewTaskEvent(
		conflictingEventID,
		taskID,
		domain.EventTaskProgress,
		"seed event conflict",
		nil,
		0,
		claimedAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent conflicting returned error: %v", err)
	}
	if err := eventStore.Append(ctx, conflictingEvent); err != nil {
		t.Fatalf("seed event Append returned error: %v", err)
	}

	expiredAt := claimedAt.Add(time.Minute)
	if _, err := transitionStore.FailNextExpiredWithEvent(ctx, expiredAt, conflictingEventID); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	stillRunning, err := taskStore.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get after rolled back cleanup returned error: %v", err)
	}
	if stillRunning.Status != domain.TaskStatusRunning || stillRunning.Version != running.Version {
		t.Fatalf("expired task failure was not rolled back: %+v", stillRunning)
	}

	failedEventID := fmt.Sprintf("event_expired_failed_%d", suffix)
	failed, err := transitionStore.FailNextExpiredWithEvent(ctx, expiredAt, failedEventID)
	if err != nil {
		t.Fatalf("FailNextExpiredWithEvent returned error: %v", err)
	}
	if failed.Status != domain.TaskStatusFailed || failed.Version != running.Version+1 {
		t.Fatalf("unexpected failed task: %+v", failed)
	}
	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 3 || events[2].ID != failedEventID || events[2].Type != domain.EventTaskFailed {
		t.Fatalf("unexpected events after expired cleanup: %+v", events)
	}
}
