package store

import (
	"context"
	"errors"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

var (
	ErrTaskNotCancelable       = errors.New("task cannot be canceled from its current status")
	ErrInvalidCancellationTime = errors.New("invalid task cancellation time")
)

type TaskCancellationStore interface {
	CancelTaskWithEvent(
		ctx context.Context,
		taskID string,
		eventID string,
		now time.Time,
	) (*TaskCancellationResult, error)
}

type TaskCancellationResult struct {
	Task     *domain.Task
	Canceled bool
}

var _ TaskCancellationStore = (*MemoryTaskTransitionStore)(nil)

func (s *MemoryTaskTransitionStore) CancelTaskWithEvent(
	ctx context.Context,
	taskID string,
	eventID string,
	now time.Time,
) (*TaskCancellationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if taskID == "" {
		return nil, ErrTaskNotFound
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}
	if now.IsZero() {
		return nil, ErrInvalidCancellationTime
	}

	s.taskStore.mu.Lock()
	defer s.taskStore.mu.Unlock()
	s.eventStore.mu.Lock()
	defer s.eventStore.mu.Unlock()

	stored, exists := s.taskStore.tasks[taskID]
	if !exists {
		return nil, ErrTaskNotFound
	}
	if stored.Status == domain.TaskStatusCanceled {
		return &TaskCancellationResult{
			Task:     cloneTask(stored),
			Canceled: false,
		}, nil
	}
	if stored.Status != domain.TaskStatusQueued &&
		stored.Status != domain.TaskStatusRetrying &&
		stored.Status != domain.TaskStatusRunning {
		return nil, ErrTaskNotCancelable
	}
	if _, exists := s.eventStore.eventsByID[eventID]; exists {
		return nil, ErrEventAlreadyExists
	}

	previous := cloneTask(stored)
	if err := stored.MoveTo(domain.TaskStatusCanceled, now); err != nil {
		return nil, err
	}
	stored.Version++
	event, err := domain.NewTaskCanceledEvent(eventID, stored, now)
	if err != nil {
		s.taskStore.tasks[taskID] = previous
		return nil, err
	}

	copiedEvent := cloneTaskEvent(event)
	s.eventStore.eventsByID[event.ID] = copiedEvent
	s.eventStore.eventsByTaskID[event.TaskID] = append(
		s.eventStore.eventsByTaskID[event.TaskID],
		copiedEvent,
	)
	return &TaskCancellationResult{
		Task:     cloneTask(stored),
		Canceled: true,
	}, nil
}
