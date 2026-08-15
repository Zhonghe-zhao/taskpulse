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

func TestMemoryEventStoreAppendAndListByTaskID(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	first := newTestEvent(t, "event_1", "task_1", domain.EventTaskCreated, 0)
	second := newTestEvent(t, "event_2", "task_1", domain.EventTaskStarted, 10)

	if err := store.Append(ctx, second); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if err := store.Append(ctx, first); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	got, err := store.ListByTaskID(ctx, "task_1")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d", len(got))
	}
	if got[0].ID != "event_1" || got[1].ID != "event_2" {
		t.Fatalf("expected events ordered by CreatedAt, got %s then %s", got[0].ID, got[1].ID)
	}
}

func TestMemoryEventStoreRejectsDuplicateEventID(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	event := newTestEvent(t, "event_1", "task_1", domain.EventTaskCreated, 0)

	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if err := store.Append(ctx, event); !errors.Is(err, ErrEventAlreadyExists) {
		t.Fatalf("expected ErrEventAlreadyExists, got %v", err)
	}
}

func TestMemoryEventStoreReturnsEmptyListForMissingTask(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()

	got, err := store.ListByTaskID(ctx, "missing")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty event list, got %d", len(got))
	}
}

func TestMemoryEventStoreCopiesAppendedEvent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	event := newTestEvent(t, "event_1", "task_1", domain.EventTaskCreated, 0)

	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	event.Message = "changed outside"
	event.Payload[0] = '['

	got, err := store.ListByTaskID(ctx, "task_1")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if got[0].Message == "changed outside" {
		t.Fatalf("expected stored event message to stay unchanged")
	}
	if !json.Valid(got[0].Payload) || got[0].Payload[0] != '{' {
		t.Fatalf("expected stored payload to be an independent JSON copy, got %s", string(got[0].Payload))
	}
}

func TestMemoryEventStoreReturnsEventCopies(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()
	event := newTestEvent(t, "event_1", "task_1", domain.EventTaskCreated, 0)

	if err := store.Append(ctx, event); err != nil {
		t.Fatalf("Append returned error: %v", err)
	}

	got, err := store.ListByTaskID(ctx, "task_1")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	got[0].Message = "changed outside"
	got[0].Payload[0] = '['

	again, err := store.ListByTaskID(ctx, "task_1")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if again[0].Message == "changed outside" {
		t.Fatalf("expected returned event to be independent from store data")
	}
	if !json.Valid(again[0].Payload) || again[0].Payload[0] != '{' {
		t.Fatalf("expected returned payload to be independent from store data, got %s", string(again[0].Payload))
	}
}

func TestMemoryEventStoreConcurrentAppendAndList(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryEventStore()

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()

			event := newTestEvent(t, fmt.Sprintf("event_%d", i), "task_1", domain.EventTaskProgress, i%101)
			if err := store.Append(ctx, event); err != nil {
				t.Errorf("Append(%s) returned error: %v", event.ID, err)
				return
			}
			if _, err := store.ListByTaskID(ctx, "task_1"); err != nil {
				t.Errorf("ListByTaskID returned error: %v", err)
			}
		}()
	}

	wg.Wait()

	got, err := store.ListByTaskID(ctx, "task_1")
	if err != nil {
		t.Fatalf("ListByTaskID returned error: %v", err)
	}
	if len(got) != workers {
		t.Fatalf("expected %d events, got %d", workers, len(got))
	}
}

func TestMemoryEventStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := NewMemoryEventStore()
	event := newTestEvent(t, "event_1", "task_1", domain.EventTaskCreated, 0)

	if err := store.Append(ctx, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func newTestEvent(t *testing.T, id, taskID string, eventType domain.EventType, progress int) *domain.TaskEvent {
	t.Helper()

	now := time.Date(2026, 6, 16, 12, 0, progress, 0, time.UTC)
	event, err := domain.NewTaskEvent(
		id,
		taskID,
		eventType,
		"task event",
		json.RawMessage(`{"detail":"ok"}`),
		progress,
		now,
	)
	if err != nil {
		t.Fatalf("NewTaskEvent returned error: %v", err)
	}
	return event
}
