package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestReaperFailsExpiredTaskAfterRetryBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()

	createdAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(
		"task_1",
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		0,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	createdEvent, err := domain.NewTaskEvent(
		"event_created",
		task.ID,
		domain.EventTaskCreated,
		"task created",
		nil,
		0,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	if err := eventStore.Append(ctx, createdEvent); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	claimedAt := createdAt.Add(time.Minute)
	if _, err := taskStore.ClaimNext(ctx, store.ClaimOptions{
		WorkerID:      "crashed_worker",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	expiredAt := claimedAt.Add(time.Minute)
	transitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	reaper := NewReaper(transitionStore)
	reaper.now = func() time.Time { return expiredAt }
	processed, err := reaper.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("ProcessNext returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected one expired task to be processed")
	}

	failed, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if failed.Status != domain.TaskStatusFailed ||
		failed.FinishedAt == nil ||
		failed.LeaseOwner != "" ||
		failed.LeaseExpiresAt != nil {
		t.Fatalf("unexpected failed task: %+v", failed)
	}
	if failed.ErrorMessage != "task lease expired and retry budget exhausted" {
		t.Fatalf("unexpected failure reason: %q", failed.ErrorMessage)
	}

	events, err := eventStore.ListByTaskID(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(events) != 2 || events[1].Type != domain.EventTaskFailed {
		t.Fatalf("unexpected task events: %+v", events)
	}
}
