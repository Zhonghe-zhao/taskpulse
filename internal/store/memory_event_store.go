package store

import (
	"context"
	"sort"
	"sync"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

type MemoryEventStore struct {
	mu             sync.RWMutex
	eventsByID     map[string]*domain.TaskEvent
	eventsByTaskID map[string][]*domain.TaskEvent
}

func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{
		eventsByID:     make(map[string]*domain.TaskEvent),
		eventsByTaskID: make(map[string][]*domain.TaskEvent),
	}
}

func (s *MemoryEventStore) Append(ctx context.Context, event *domain.TaskEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event == nil {
		return ErrNilEvent
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.eventsByID[event.ID]; exists {
		return ErrEventAlreadyExists
	}

	copied := cloneTaskEvent(event)
	s.eventsByID[event.ID] = copied
	s.eventsByTaskID[event.TaskID] = append(s.eventsByTaskID[event.TaskID], copied)

	return nil
}

func (s *MemoryEventStore) ListByTaskID(ctx context.Context, taskID string) ([]*domain.TaskEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.eventsByTaskID[taskID]
	copied := make([]*domain.TaskEvent, 0, len(events))
	for _, event := range events {
		copied = append(copied, cloneTaskEvent(event))
	}

	sort.SliceStable(copied, func(i, j int) bool {
		return copied[i].CreatedAt.Before(copied[j].CreatedAt)
	})

	return copied, nil
}

func cloneTaskEvent(event *domain.TaskEvent) *domain.TaskEvent {
	if event == nil {
		return nil
	}

	copied := *event
	copied.Payload = cloneRawMessage(event.Payload)

	return &copied
}
