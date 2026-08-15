package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	storeerrors "github.com/zhaozhonghe/taskpulse/internal/store"
)

var _ storeerrors.TaskStatsStore = (*MySQLTaskStore)(nil)
var _ storeerrors.FilteredTaskStatsStore = (*MySQLTaskStore)(nil)

func (s *MySQLTaskStore) SnapshotTaskStats(
	ctx context.Context,
	now time.Time,
) (*storeerrors.TaskStatsSnapshot, error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	snapshot := storeerrors.NewTaskStatsSnapshot()
	if err := scanStatusCounts(ctx, s.db, snapshot); err != nil {
		return nil, err
	}
	if err := scanAvailableStats(ctx, s.db, snapshot, now); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *MySQLTaskStore) SnapshotFilteredTaskStats(
	ctx context.Context,
	now time.Time,
	filter storeerrors.TaskStatsFilter,
) (*storeerrors.TaskStatsSnapshot, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	snapshot := storeerrors.NewTaskStatsSnapshot()
	if err := scanFilteredStatusCounts(ctx, s.db, snapshot, filter); err != nil {
		return nil, err
	}
	if err := scanFilteredAvailableStats(ctx, s.db, snapshot, now, filter); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func appendTaskStatsFilter(query *strings.Builder, args *[]any, filter storeerrors.TaskStatsFilter) {
	if filter.Workflow != "" {
		query.WriteString(" AND workflow = ?")
		*args = append(*args, filter.Workflow)
	}
	if filter.Status != "" {
		query.WriteString(" AND status = ?")
		*args = append(*args, string(filter.Status))
	}
}

func scanFilteredStatusCounts(
	ctx context.Context,
	db *sql.DB,
	snapshot *storeerrors.TaskStatsSnapshot,
	filter storeerrors.TaskStatsFilter,
) error {
	var query strings.Builder
	query.WriteString("SELECT status, COUNT(*) FROM tasks WHERE 1 = 1")
	args := make([]any, 0, 2)
	appendTaskStatsFilter(&query, &args, filter)
	query.WriteString(" GROUP BY status")
	rows, err := db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return fmt.Errorf("select filtered task status counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return fmt.Errorf("scan filtered task status count: %w", err)
		}
		snapshot.StatusCounts[domain.TaskStatus(status)] = count
	}
	return rows.Err()
}

func scanFilteredAvailableStats(
	ctx context.Context,
	db *sql.DB,
	snapshot *storeerrors.TaskStatsSnapshot,
	now time.Time,
	filter storeerrors.TaskStatsFilter,
) error {
	var query strings.Builder
	query.WriteString("SELECT status, COUNT(*), MIN(available_at) FROM tasks WHERE status IN (?, ?) AND available_at <= ?")
	args := []any{string(domain.TaskStatusQueued), string(domain.TaskStatusRetrying), now}
	appendTaskStatsFilter(&query, &args, filter)
	query.WriteString(" GROUP BY status")
	rows, err := db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return fmt.Errorf("select filtered available task stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		var oldest time.Time
		if err := rows.Scan(&status, &count, &oldest); err != nil {
			return fmt.Errorf("scan filtered available task stats: %w", err)
		}
		taskStatus := domain.TaskStatus(status)
		snapshot.AvailableCounts[taskStatus] = count
		snapshot.OldestAvailableAge[taskStatus] = now.Sub(oldest.UTC())
	}
	return rows.Err()
}

func scanStatusCounts(
	ctx context.Context,
	db *sql.DB,
	snapshot *storeerrors.TaskStatsSnapshot,
) error {
	rows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*)
		FROM tasks
		GROUP BY status`)
	if err != nil {
		return fmt.Errorf("select task status counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return fmt.Errorf("scan task status count: %w", err)
		}
		snapshot.StatusCounts[domain.TaskStatus(status)] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task status counts: %w", err)
	}
	return nil
}

func scanAvailableStats(
	ctx context.Context,
	db *sql.DB,
	snapshot *storeerrors.TaskStatsSnapshot,
	now time.Time,
) error {
	rows, err := db.QueryContext(ctx, `
		SELECT status, COUNT(*), MIN(available_at)
		FROM tasks
		WHERE status IN (?, ?)
		  AND available_at <= ?
		GROUP BY status`,
		string(domain.TaskStatusQueued),
		string(domain.TaskStatusRetrying),
		now,
	)
	if err != nil {
		return fmt.Errorf("select available task stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		var oldest time.Time
		if err := rows.Scan(&status, &count, &oldest); err != nil {
			return fmt.Errorf("scan available task stats: %w", err)
		}
		taskStatus := domain.TaskStatus(status)
		snapshot.AvailableCounts[taskStatus] = count
		snapshot.OldestAvailableAge[taskStatus] = now.Sub(oldest.UTC())
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate available task stats: %w", err)
	}
	return nil
}
