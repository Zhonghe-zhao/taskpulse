package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

const insertEventQuery = `
INSERT INTO task_events (
    id,
    task_id,
    type,
    message,
    payload_json,
    progress,
    created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`

const listEventsByTaskIDQuery = `
SELECT
    id,
    task_id,
    type,
    message,
    payload_json,
    progress,
    created_at
FROM task_events
WHERE task_id = ?
ORDER BY created_at, id`

type MySQLEventStore struct {
	db *sql.DB
}

var _ storeerrors.EventStore = (*MySQLEventStore)(nil)

func NewEventStore(db *sql.DB) (*MySQLEventStore, error) {
	if db == nil {
		return nil, errors.New("mysql event store database is nil")
	}
	return &MySQLEventStore{db: db}, nil
}

func (s *MySQLEventStore) Append(ctx context.Context, event *domain.TaskEvent) error {
	return insertEvent(ctx, s.db, event)
}

func insertEvent(ctx context.Context, executor sqlExecutor, event *domain.TaskEvent) error {
	if event == nil {
		return storeerrors.ErrNilEvent
	}

	_, err := executor.ExecContext(
		ctx,
		insertEventQuery,
		event.ID,
		event.TaskID,
		string(event.Type),
		event.Message,
		nullableJSON(event.Payload),
		event.Progress,
		event.CreatedAt.UTC(),
	)
	if err == nil {
		return nil
	}

	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) {
		switch mysqlError.Number {
		case 1062:
			return storeerrors.ErrEventAlreadyExists
		case 1452:
			return storeerrors.ErrTaskNotFound
		}
	}
	return fmt.Errorf("insert event %q for task %q: %w", event.ID, event.TaskID, err)
}

func (s *MySQLEventStore) ListByTaskID(ctx context.Context, taskID string) ([]*domain.TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, listEventsByTaskIDQuery, taskID)
	if err != nil {
		return nil, fmt.Errorf("list events for task %q: %w", taskID, err)
	}
	defer rows.Close()

	events := make([]*domain.TaskEvent, 0)
	for rows.Next() {
		var event domain.TaskEvent
		var eventType string
		var payloadJSON []byte
		if err := rows.Scan(
			&event.ID,
			&event.TaskID,
			&eventType,
			&event.Message,
			&payloadJSON,
			&event.Progress,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event for task %q: %w", taskID, err)
		}
		event.Type = domain.EventType(eventType)
		event.Payload = cloneJSON(payloadJSON)
		event.CreatedAt = event.CreatedAt.UTC()
		events = append(events, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events for task %q: %w", taskID, err)
	}
	return events, nil
}
