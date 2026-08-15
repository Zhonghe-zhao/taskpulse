package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

var (
	ErrTaskEventMismatch           = errors.New("task event does not belong to task")
	ErrIdempotencyConflict         = errors.New("idempotency key is already used by a different request")
	ErrIdempotencyKeyAlreadyExists = errors.New("idempotency key already exists")
)

type TaskCreationStore interface {
	CreateTaskWithEvent(
		ctx context.Context,
		task *domain.Task,
		event *domain.TaskEvent,
	) (*TaskCreationResult, error)
}

type TaskCreationResult struct {
	Task    *domain.Task
	Created bool
}

type MemoryTaskCreationStore struct {
	taskStore  *MemoryTaskStore
	eventStore *MemoryEventStore
}

var _ TaskCreationStore = (*MemoryTaskCreationStore)(nil)

func NewMemoryTaskCreationStore(
	taskStore *MemoryTaskStore,
	eventStore *MemoryEventStore,
) *MemoryTaskCreationStore {
	return &MemoryTaskCreationStore{
		taskStore:  taskStore,
		eventStore: eventStore,
	}
}

func (s *MemoryTaskCreationStore) CreateTaskWithEvent(
	ctx context.Context,
	task *domain.Task,
	event *domain.TaskEvent,
) (*TaskCreationResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrNilTask
	}
	if event == nil {
		return nil, ErrNilEvent
	}
	if event.TaskID != task.ID {
		return nil, ErrTaskEventMismatch
	}

	s.taskStore.mu.Lock()
	defer s.taskStore.mu.Unlock()
	s.eventStore.mu.Lock()
	defer s.eventStore.mu.Unlock()

	if task.IdempotencyKey != "" {
		indexKey := idempotencyIndexKey(task.Workflow, task.IdempotencyKey)
		if taskID, exists := s.taskStore.taskIDsByIdempotencyKey[indexKey]; exists {
			existing := s.taskStore.tasks[taskID]
			if !SameTaskCreationRequest(existing, task) {
				return nil, ErrIdempotencyConflict
			}
			return &TaskCreationResult{
				Task:    cloneTask(existing),
				Created: false,
			}, nil
		}
	}
	if _, exists := s.taskStore.tasks[task.ID]; exists {
		return nil, ErrTaskAlreadyExists
	}
	if _, exists := s.eventStore.eventsByID[event.ID]; exists {
		return nil, ErrEventAlreadyExists
	}

	copiedTask := cloneTask(task)
	copiedEvent := cloneTaskEvent(event)
	s.taskStore.tasks[task.ID] = copiedTask
	if task.IdempotencyKey != "" {
		s.taskStore.taskIDsByIdempotencyKey[idempotencyIndexKey(task.Workflow, task.IdempotencyKey)] = task.ID
	}
	s.eventStore.eventsByID[event.ID] = copiedEvent
	s.eventStore.eventsByTaskID[event.TaskID] = append(
		s.eventStore.eventsByTaskID[event.TaskID],
		copiedEvent,
	)
	return &TaskCreationResult{
		Task:    cloneTask(copiedTask),
		Created: true,
	}, nil
}

func SameTaskCreationRequest(left, right *domain.Task) bool {
	if left == nil || right == nil {
		return false
	}
	if left.IdempotencyKey != right.IdempotencyKey ||
		left.Workflow != right.Workflow ||
		left.MaxRetries != right.MaxRetries {
		return false
	}

	var leftInput any
	var rightInput any
	if err := decodeJSONValue(left.Input, &leftInput); err != nil {
		return bytes.Equal(left.Input, right.Input)
	}
	if err := decodeJSONValue(right.Input, &rightInput); err != nil {
		return false
	}
	return reflect.DeepEqual(leftInput, rightInput)
}

func decodeJSONValue(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains more than one value")
	}
	return nil
}
