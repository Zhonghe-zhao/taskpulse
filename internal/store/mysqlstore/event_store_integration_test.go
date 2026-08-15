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

func TestMySQLEventStoreAppendAndListIntegration(t *testing.T) {
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

	suffix := time.Now().UnixNano()
	taskID := fmt.Sprintf("task_mysql_events_%d", suffix)
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	task, err := domain.NewTask(
		taskID,
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		3,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	later, err := domain.NewTaskEvent(
		fmt.Sprintf("event_later_%d", suffix),
		taskID,
		domain.EventTaskStarted,
		"task started",
		json.RawMessage(`{"worker":"worker_1"}`),
		10,
		createdAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewTaskEvent later returned error: %v", err)
	}
	earlier, err := domain.NewTaskEvent(
		fmt.Sprintf("event_earlier_%d", suffix),
		taskID,
		domain.EventTaskCreated,
		"task created",
		json.RawMessage(`{"source":"integration_test"}`),
		0,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent earlier returned error: %v", err)
	}

	if err := eventStore.Append(ctx, later); err != nil {
		t.Fatalf("Append later event returned error: %v", err)
	}
	if err := eventStore.Append(ctx, earlier); err != nil {
		t.Fatalf("Append earlier event returned error: %v", err)
	}

	events, err := eventStore.ListByTaskID(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != earlier.ID || events[1].ID != later.ID {
		t.Fatalf("events are not ordered by creation time: %+v", events)
	}
	if string(events[0].Payload) != string(earlier.Payload) {
		t.Fatalf("expected payload %s, got %s", earlier.Payload, events[0].Payload)
	}
	if !events[0].CreatedAt.Equal(earlier.CreatedAt) {
		t.Fatalf("expected created_at %s, got %s", earlier.CreatedAt, events[0].CreatedAt)
	}

	if err := eventStore.Append(ctx, earlier); !errors.Is(err, storeerrors.ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
	missing, err := eventStore.ListByTaskID(ctx, "task_without_events")
	if err != nil {
		t.Fatalf("ListByTaskID missing task returned error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("expected empty event list, got %d", len(missing))
	}
}

func TestMySQLEventStoreRejectsMissingTaskIntegration(t *testing.T) {
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

	eventStore, err := NewEventStore(db)
	if err != nil {
		t.Fatalf("NewEventStore returned error: %v", err)
	}
	missingTaskID := fmt.Sprintf("task_missing_%d", time.Now().UnixNano())
	event, err := domain.NewTaskEvent(
		fmt.Sprintf("event_missing_task_%d", time.Now().UnixNano()),
		missingTaskID,
		domain.EventTaskCreated,
		"task created",
		nil,
		0,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(ctx, event); !errors.Is(err, storeerrors.ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}
