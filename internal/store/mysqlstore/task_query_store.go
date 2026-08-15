package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

var _ storeerrors.TaskQueryStore = (*MySQLTaskStore)(nil)

func (s *MySQLTaskStore) ListTasks(
	ctx context.Context,
	options storeerrors.ListTasksOptions,
) ([]storeerrors.TaskListItem, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}

	var query strings.Builder
	query.WriteString(`
SELECT
    id,
    workflow,
    status,
    error_message,
    progress,
    retry_count,
    max_retries,
    lease_owner,
    lease_expires_at,
    created_at,
    updated_at
FROM tasks
WHERE 1 = 1`)
	args := make([]any, 0, 7)
	if options.Workflow != "" {
		query.WriteString("\n  AND workflow = ?")
		args = append(args, options.Workflow)
	}
	if options.Status != "" {
		query.WriteString("\n  AND status = ?")
		args = append(args, string(options.Status))
	}
	if options.BeforeCreatedAt != nil {
		query.WriteString("\n  AND (created_at < ? OR (created_at = ? AND id < ?))")
		before := options.BeforeCreatedAt.UTC()
		args = append(args, before, before, options.BeforeID)
	}
	query.WriteString("\nORDER BY created_at DESC, id DESC\nLIMIT ?")
	args = append(args, options.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	items := make([]storeerrors.TaskListItem, 0, options.Limit)
	for rows.Next() {
		var item storeerrors.TaskListItem
		var status string
		var errorMessage sql.NullString
		var leaseOwner sql.NullString
		var leaseExpiresAt sql.NullTime
		if err := rows.Scan(
			&item.ID,
			&item.Workflow,
			&status,
			&errorMessage,
			&item.Progress,
			&item.RetryCount,
			&item.MaxRetries,
			&leaseOwner,
			&leaseExpiresAt,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan task list item: %w", err)
		}
		item.Status = domain.TaskStatus(status)
		if errorMessage.Valid {
			item.ErrorMessage = errorMessage.String
		}
		if leaseOwner.Valid {
			item.LeaseOwner = leaseOwner.String
		}
		item.LeaseExpiresAt = timePointer(leaseExpiresAt)
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = item.UpdatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task list: %w", err)
	}
	return items, nil
}
