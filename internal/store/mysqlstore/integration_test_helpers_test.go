package mysqlstore

import (
	"context"
	"database/sql"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

// cleanupActiveTasks removes queued, retrying and running tasks left by earlier
// integration runs so ClaimNext only observes rows created in the current test.
func cleanupActiveTasks(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(
		ctx,
		`DELETE FROM tasks WHERE status IN (?, ?, ?)`,
		string(domain.TaskStatusQueued),
		string(domain.TaskStatusRetrying),
		string(domain.TaskStatusRunning),
	)
	return err
}
