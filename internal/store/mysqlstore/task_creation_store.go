package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	storeerrors "github.com/zhaozhonghe/taskpulse/internal/store"
)

type MySQLTaskCreationStore struct {
	db *sql.DB
}

var _ storeerrors.TaskCreationStore = (*MySQLTaskCreationStore)(nil)

func NewTaskCreationStore(db *sql.DB) (*MySQLTaskCreationStore, error) {
	if db == nil {
		return nil, errors.New("mysql task creation store database is nil")
	}
	return &MySQLTaskCreationStore{db: db}, nil
}

func (s *MySQLTaskCreationStore) CreateTaskWithEvent(
	ctx context.Context,
	task *domain.Task,
	event *domain.TaskEvent,
) (*storeerrors.TaskCreationResult, error) {
	if task == nil {
		return nil, storeerrors.ErrNilTask
	}
	if event == nil {
		return nil, storeerrors.ErrNilEvent
	}
	if event.TaskID != task.ID {
		return nil, storeerrors.ErrTaskEventMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin task creation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertTask(ctx, tx, task); err != nil {
		if errors.Is(err, storeerrors.ErrIdempotencyKeyAlreadyExists) {
			_ = tx.Rollback()
			existing, resolveErr := s.resolveIdempotentReplay(ctx, task)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return &storeerrors.TaskCreationResult{
				Task:    existing,
				Created: false,
			}, nil
		}
		return nil, err
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task %q creation: %w", task.ID, err)
	}
	return &storeerrors.TaskCreationResult{
		Task:    task,
		Created: true,
	}, nil
}

func (s *MySQLTaskCreationStore) resolveIdempotentReplay(
	ctx context.Context,
	requested *domain.Task,
) (*domain.Task, error) {
	taskStore := &MySQLTaskStore{db: s.db}
	existing, err := taskStore.getByWorkflowAndIdempotencyKey(ctx, requested.Workflow, requested.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if !storeerrors.SameTaskCreationRequest(existing, requested) {
		return nil, storeerrors.ErrIdempotencyConflict
	}
	return existing, nil
}
