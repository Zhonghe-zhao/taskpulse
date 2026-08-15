package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

type MySQLTaskTransitionStore struct {
	db *sql.DB
}

var _ storeerrors.TaskTransitionStore = (*MySQLTaskTransitionStore)(nil)

func NewTaskTransitionStore(db *sql.DB) (*MySQLTaskTransitionStore, error) {
	if db == nil {
		return nil, errors.New("mysql task transition store database is nil")
	}
	return &MySQLTaskTransitionStore{db: db}, nil
}

func (s *MySQLTaskTransitionStore) ClaimNextWithEvent(
	ctx context.Context,
	options storeerrors.ClaimOptions,
	eventID string,
) (_ *domain.Task, err error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if eventID == "" {
		return nil, storeerrors.ErrInvalidEventID
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin task claim transition transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	exists, err := eventExists(ctx, tx, eventID)
	if err != nil {
		return nil, fmt.Errorf("check claim event %q: %w", eventID, err)
	}
	if exists {
		return nil, storeerrors.ErrEventAlreadyExists
	}

	task, claimKind, err := claimNextInTx(ctx, tx, options)
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskClaimedEvent(eventID, task, claimKind, options.Now.UTC())
	if err != nil {
		return nil, err
	}
	if err = insertEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task %q claim transition: %w", task.ID, err)
	}
	return task, nil
}

func (s *MySQLTaskTransitionStore) FailNextExpiredWithEvent(
	ctx context.Context,
	now time.Time,
	eventID string,
) (_ *domain.Task, err error) {
	if now.IsZero() {
		return nil, storeerrors.ErrInvalidCleanupTime
	}
	if eventID == "" {
		return nil, storeerrors.ErrInvalidEventID
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin expired task transition transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	exists, err := eventExists(ctx, tx, eventID)
	if err != nil {
		return nil, fmt.Errorf("check expired task event %q: %w", eventID, err)
	}
	if exists {
		return nil, storeerrors.ErrEventAlreadyExists
	}

	task, err := failNextExpiredInTx(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskExpiredEvent(eventID, task, now.UTC())
	if err != nil {
		return nil, err
	}
	if err = insertEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit expired task %q transition: %w", task.ID, err)
	}
	return task, nil
}

func (s *MySQLTaskTransitionStore) UpdateTaskWithEvent(
	ctx context.Context,
	task *domain.Task,
	event *domain.TaskEvent,
) (err error) {
	if task == nil {
		return storeerrors.ErrNilTask
	}
	if event == nil {
		return storeerrors.ErrNilEvent
	}
	if event.TaskID != task.ID {
		return storeerrors.ErrTaskEventMismatch
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin task transition transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	exists, err := eventExists(ctx, tx, event.ID)
	if err != nil {
		return fmt.Errorf("check transition event %q: %w", event.ID, err)
	}
	if exists {
		return storeerrors.ErrEventAlreadyExists
	}

	if err = updateTask(ctx, tx, task); err != nil {
		return err
	}
	if err = insertEvent(ctx, tx, event); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit task %q transition: %w", task.ID, err)
	}
	return nil
}
