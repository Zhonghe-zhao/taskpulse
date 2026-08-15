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

const getTaskForUpdateQuery = getTaskQuery + "\nFOR UPDATE"

var _ storeerrors.TaskCancellationStore = (*MySQLTaskTransitionStore)(nil)

func (s *MySQLTaskTransitionStore) CancelTaskWithEvent(
	ctx context.Context,
	taskID string,
	eventID string,
	now time.Time,
) (*storeerrors.TaskCancellationResult, error) {
	if taskID == "" {
		return nil, storeerrors.ErrTaskNotFound
	}
	if eventID == "" {
		return nil, storeerrors.ErrInvalidEventID
	}
	if now.IsZero() {
		return nil, storeerrors.ErrInvalidCancellationTime
	}
	now = now.UTC()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin task cancellation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	task, err := scanTask(tx.QueryRowContext(ctx, getTaskForUpdateQuery, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storeerrors.ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select task %q for cancellation: %w", taskID, err)
	}
	if task.Status == domain.TaskStatusCanceled {
		return &storeerrors.TaskCancellationResult{
			Task:     task,
			Canceled: false,
		}, nil
	}
	if task.Status != domain.TaskStatusQueued &&
		task.Status != domain.TaskStatusRetrying &&
		task.Status != domain.TaskStatusRunning {
		return nil, storeerrors.ErrTaskNotCancelable
	}

	exists, err := eventExists(ctx, tx, eventID)
	if err != nil {
		return nil, fmt.Errorf("check cancellation event %q: %w", eventID, err)
	}
	if exists {
		return nil, storeerrors.ErrEventAlreadyExists
	}
	if err := task.MoveTo(domain.TaskStatusCanceled, now); err != nil {
		return nil, err
	}
	event, err := domain.NewTaskCanceledEvent(eventID, task, now)
	if err != nil {
		return nil, err
	}
	if err := updateTask(ctx, tx, task); err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, event); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task %q cancellation: %w", taskID, err)
	}

	task.Version++
	return &storeerrors.TaskCancellationResult{
		Task:     task,
		Canceled: true,
	}, nil
}
