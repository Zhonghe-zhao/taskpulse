package mysqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

const insertTaskQuery = `
INSERT INTO tasks (
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    created_at,
    updated_at,
    started_at,
    finished_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const getTaskQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE id = ?`

const getTaskByWorkflowAndIdempotencyKeyQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE workflow = ?
  AND idempotency_key = ?`

const claimNextTaskQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE status IN (?, ?)
  AND available_at <= ?
ORDER BY available_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED` //它解决多个 Worker 并发领取的问题。

const claimExpiredTaskQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE status = ?
  AND lease_expires_at <= ?
  AND retry_count < max_retries
ORDER BY lease_expires_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

const claimNextTaskByWorkflowQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE status IN (?, ?)
  AND workflow = ?
  AND available_at <= ?
ORDER BY available_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

const claimExpiredTaskByWorkflowQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE status = ?
  AND workflow = ?
  AND lease_expires_at <= ?
  AND retry_count < max_retries
ORDER BY lease_expires_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

const updateTaskQuery = `
UPDATE tasks
SET
    status = ?,
    result_json = ?,
    error_message = ?,
    progress = ?,
    retry_count = ?,
    max_retries = ?,
    available_at = ?,
    lease_owner = ?,
    lease_expires_at = ?,
    last_heartbeat_at = ?,
    updated_at = ?,
    started_at = ?,
    finished_at = ?,
    version = version + 1
WHERE id = ?
  AND version = ?
  AND idempotency_key <=> ?`

const renewLeaseQuery = `
UPDATE tasks
SET
    lease_expires_at = ?,
    last_heartbeat_at = ?,
    updated_at = ?
WHERE id = ?
  AND status = ?
  AND lease_owner = ?
  AND lease_expires_at > ?`

const selectExpiredExhaustedTaskQuery = `
SELECT
    id,
    idempotency_key,
    workflow,
    status,
    input_json,
    result_json,
    error_message,
    progress,
    retry_count,
    max_retries,
    available_at,
    lease_owner,
    lease_expires_at,
    last_heartbeat_at,
    version,
    created_at,
    updated_at,
    started_at,
    finished_at
FROM tasks
WHERE status = ?
  AND lease_expires_at <= ?
  AND retry_count >= max_retries
ORDER BY lease_expires_at, created_at, id
LIMIT 1
FOR UPDATE SKIP LOCKED`

type MySQLTaskStore struct {
	db *sql.DB
}

var _ storeerrors.TaskStore = (*MySQLTaskStore)(nil)

func NewTaskStore(db *sql.DB) (*MySQLTaskStore, error) {
	if db == nil {
		return nil, errors.New("mysql task store database is nil")
	}
	return &MySQLTaskStore{db: db}, nil
}

func (s *MySQLTaskStore) Create(ctx context.Context, task *domain.Task) error {
	return insertTask(ctx, s.db, task)
}

func insertTask(ctx context.Context, executor sqlExecutor, task *domain.Task) error {
	if task == nil {
		return storeerrors.ErrNilTask
	}

	_, err := executor.ExecContext(
		ctx,
		insertTaskQuery,
		task.ID,
		nullableString(task.IdempotencyKey),
		task.Workflow,
		string(task.Status),
		[]byte(task.Input),
		nullableJSON(task.Result),
		nullableString(task.ErrorMessage),
		task.Progress,
		task.RetryCount,
		task.MaxRetries,
		task.AvailableAt.UTC(),
		task.CreatedAt.UTC(),
		task.UpdatedAt.UTC(),
		nullableTime(task.StartedAt),
		nullableTime(task.FinishedAt),
	)
	if err != nil {
		var mysqlError *mysqldriver.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			if strings.Contains(mysqlError.Message, "uk_tasks_workflow_idempotency") ||
				strings.Contains(mysqlError.Message, "uk_tasks_idempotency_key") {
				return storeerrors.ErrIdempotencyKeyAlreadyExists
			}
			return storeerrors.ErrTaskAlreadyExists
		}
		return fmt.Errorf("insert task %q: %w", task.ID, err)
	}
	return nil
}

func (s *MySQLTaskStore) Get(ctx context.Context, id string) (*domain.Task, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, getTaskQuery, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storeerrors.ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select task %q: %w", id, err)
	}
	return task, nil
}

func (s *MySQLTaskStore) getByWorkflowAndIdempotencyKey(
	ctx context.Context,
	workflow string,
	idempotencyKey string,
) (*domain.Task, error) {
	task, err := scanTask(s.db.QueryRowContext(
		ctx,
		getTaskByWorkflowAndIdempotencyKeyQuery,
		workflow,
		idempotencyKey,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storeerrors.ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select task by idempotency key: %w", err)
	}
	return task, nil
}

func (s *MySQLTaskStore) ClaimNext(ctx context.Context, options storeerrors.ClaimOptions) (_ *domain.Task, err error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin claim task transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	task, _, err := claimNextInTx(ctx, tx, options)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claimed task %q: %w", task.ID, err)
	}
	return task, nil
}

func claimNextInTx(
	ctx context.Context,
	tx *sql.Tx,
	options storeerrors.ClaimOptions,
) (*domain.Task, domain.ClaimKind, error) {
	expiredQuery := claimExpiredTaskQuery
	expiredArgs := []any{string(domain.TaskStatusRunning), options.Now.UTC()}
	nextQuery := claimNextTaskQuery
	nextArgs := []any{string(domain.TaskStatusQueued), string(domain.TaskStatusRetrying), options.Now.UTC()}
	if options.Workflow != "" {
		expiredQuery = claimExpiredTaskByWorkflowQuery
		expiredArgs = []any{string(domain.TaskStatusRunning), options.Workflow, options.Now.UTC()}
		nextQuery = claimNextTaskByWorkflowQuery
		nextArgs = []any{string(domain.TaskStatusQueued), string(domain.TaskStatusRetrying), options.Workflow, options.Now.UTC()}
	}
	now := options.Now.UTC() //统一时间
	//优先查询过期任务
	task, err := scanTask(tx.QueryRowContext(
		ctx,
		expiredQuery,
		expiredArgs...,
	))
	claimKind := domain.ClaimRecovery // 优先接管租约过期的 running 任务。
	if errors.Is(err, sql.ErrNoRows) {
		// 没有可恢复任务时，再查询已到期的 queued/retrying 任务。
		task, err = scanTask(tx.QueryRowContext(
			ctx,
			nextQuery,
			nextArgs...,
		))
		if err == nil {
			if task.Status == domain.TaskStatusRetrying {
				claimKind = domain.ClaimRetry
			} else {
				claimKind = domain.ClaimInitial
			}
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", storeerrors.ErrNoTaskAvailable
	}
	if err != nil {
		return nil, "", fmt.Errorf("select next task for claim: %w", err)
	}
	if claimKind == domain.ClaimRecovery {
		task.RetryCount++
		task.UpdatedAt = now
	} else if err = task.MoveTo(domain.TaskStatusRunning, now); err != nil {
		return nil, "", fmt.Errorf("move claimed task %q to running: %w", task.ID, err)
	}
	leaseExpiresAt := now.Add(options.LeaseDuration)
	task.LeaseOwner = options.WorkerID
	task.LeaseExpiresAt = &leaseExpiresAt
	task.LastHeartbeatAt = nil

	result, err := tx.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = ?, retry_count = ?, updated_at = ?, started_at = ?,
		     lease_owner = ?, lease_expires_at = ?, last_heartbeat_at = NULL, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(task.Status),
		task.RetryCount,
		task.UpdatedAt,
		nullableTime(task.StartedAt),
		task.LeaseOwner,
		nullableTime(task.LeaseExpiresAt),
		task.ID,
		task.Version,
	)
	if err != nil {
		return nil, "", fmt.Errorf("mark claimed task %q as running: %w", task.ID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, "", fmt.Errorf("read claimed row count for task %q: %w", task.ID, err)
	}
	if rowsAffected != 1 {
		return nil, "", fmt.Errorf(
			"claim task %q at version %d affected %d rows",
			task.ID,
			task.Version,
			rowsAffected,
		)
	}

	task.Version++
	return task, claimKind, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*domain.Task, error) {
	var task domain.Task
	var idempotencyKey sql.NullString
	var status string
	var inputJSON []byte
	var resultJSON []byte
	var errorMessage sql.NullString
	var leaseOwner sql.NullString
	var leaseExpiresAt sql.NullTime
	var lastHeartbeatAt sql.NullTime
	var startedAt sql.NullTime
	var finishedAt sql.NullTime

	err := row.Scan(
		&task.ID,
		&idempotencyKey,
		&task.Workflow,
		&status,
		&inputJSON,
		&resultJSON,
		&errorMessage,
		&task.Progress,
		&task.RetryCount,
		&task.MaxRetries,
		&task.AvailableAt,
		&leaseOwner,
		&leaseExpiresAt,
		&lastHeartbeatAt,
		&task.Version,
		&task.CreatedAt,
		&task.UpdatedAt,
		&startedAt,
		&finishedAt,
	)
	if err != nil {
		return nil, err
	}

	task.Status = domain.TaskStatus(status)
	if idempotencyKey.Valid {
		task.IdempotencyKey = idempotencyKey.String
	}
	task.Input = cloneJSON(inputJSON)
	task.Result = cloneJSON(resultJSON)
	if errorMessage.Valid {
		task.ErrorMessage = errorMessage.String
	}
	if leaseOwner.Valid {
		task.LeaseOwner = leaseOwner.String
	}
	task.LeaseExpiresAt = timePointer(leaseExpiresAt)
	task.LastHeartbeatAt = timePointer(lastHeartbeatAt)
	task.AvailableAt = task.AvailableAt.UTC()
	task.CreatedAt = task.CreatedAt.UTC()
	task.UpdatedAt = task.UpdatedAt.UTC()
	task.StartedAt = timePointer(startedAt)
	task.FinishedAt = timePointer(finishedAt)
	return &task, nil
}

func (s *MySQLTaskStore) Update(ctx context.Context, task *domain.Task) error {
	return updateTask(ctx, s.db, task)
}

func updateTask(ctx context.Context, executor sqlQueryExecutor, task *domain.Task) error {
	if task == nil {
		return storeerrors.ErrNilTask
	}

	result, err := executor.ExecContext(
		ctx,
		updateTaskQuery,
		string(task.Status),
		nullableJSON(task.Result),
		nullableString(task.ErrorMessage),
		task.Progress,
		task.RetryCount,
		task.MaxRetries,
		task.AvailableAt.UTC(),
		nullableString(task.LeaseOwner),
		nullableTime(task.LeaseExpiresAt),
		nullableTime(task.LastHeartbeatAt),
		task.UpdatedAt.UTC(),
		nullableTime(task.StartedAt),
		nullableTime(task.FinishedAt),
		task.ID,
		task.Version,
		nullableString(task.IdempotencyKey),
	)
	if err != nil {
		return fmt.Errorf("update task %q at version %d: %w", task.ID, task.Version, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated row count for task %q: %w", task.ID, err)
	}
	if rowsAffected == 1 {
		return nil
	}
	if rowsAffected > 1 {
		return fmt.Errorf("update task %q affected %d rows", task.ID, rowsAffected)
	}

	var exists int
	err = executor.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id = ?", task.ID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return storeerrors.ErrTaskNotFound
	}
	if err != nil {
		return fmt.Errorf("check task %q after versioned update: %w", task.ID, err)
	}
	return storeerrors.ErrTaskConflict
}

func (s *MySQLTaskStore) RenewLease(ctx context.Context, options storeerrors.RenewLeaseOptions) error {
	if err := options.Validate(); err != nil {
		return err
	}

	now := options.Now.UTC()
	leaseExpiresAt := now.Add(options.LeaseDuration)
	result, err := s.db.ExecContext(
		ctx,
		renewLeaseQuery,
		leaseExpiresAt,
		now,
		now,
		options.TaskID,
		string(domain.TaskStatusRunning),
		options.WorkerID,
		now,
	)
	if err != nil {
		return fmt.Errorf("renew lease for task %q: %w", options.TaskID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read renewed lease row count for task %q: %w", options.TaskID, err)
	}
	if rowsAffected != 1 {
		return storeerrors.ErrLeaseLost
	}
	return nil
}

// 找出下一条需要清理的过期任务，并将它标记为失败。
func (s *MySQLTaskStore) FailNextExpired(ctx context.Context, now time.Time) (_ *domain.Task, err error) {
	if now.IsZero() {
		return nil, storeerrors.ErrInvalidCleanupTime
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin expired task cleanup transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	task, err := failNextExpiredInTx(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit expired task %q cleanup: %w", task.ID, err)
	}
	return task, nil
}

func failNextExpiredInTx(
	ctx context.Context,
	tx *sql.Tx,
	now time.Time,
) (*domain.Task, error) {
	now = now.UTC()
	task, err := scanTask(tx.QueryRowContext(
		ctx,
		selectExpiredExhaustedTaskQuery,
		string(domain.TaskStatusRunning),
		now,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storeerrors.ErrNoExpiredTask
	}
	if err != nil {
		return nil, fmt.Errorf("select expired exhausted task: %w", err)
	}

	task.ErrorMessage = "task lease expired and retry budget exhausted"
	if err = task.MoveTo(domain.TaskStatusFailed, now); err != nil {
		return nil, fmt.Errorf("fail expired task %q: %w", task.ID, err)
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE tasks
		 SET status = ?, error_message = ?, updated_at = ?, finished_at = ?,
		     lease_owner = NULL, lease_expires_at = NULL, version = version + 1
		 WHERE id = ? AND version = ?`,
		string(task.Status),
		task.ErrorMessage,
		task.UpdatedAt,
		nullableTime(task.FinishedAt),
		task.ID,
		task.Version,
	)
	if err != nil {
		return nil, fmt.Errorf("persist expired task %q failure: %w", task.ID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read expired task %q cleanup row count: %w", task.ID, err)
	}
	if rowsAffected != 1 {
		return nil, fmt.Errorf(
			"cleanup expired task %q at version %d affected %d rows",
			task.ID,
			task.Version,
			rowsAffected,
		)
	}

	task.Version++
	return task, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func timePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func cloneJSON(value []byte) json.RawMessage {
	if value == nil {
		return nil
	}
	// MySQL JSON columns may reformat whitespace on read; compact keeps a stable byte form.
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, value); err == nil {
		return append(json.RawMessage(nil), compacted.Bytes()...)
	}
	return append(json.RawMessage(nil), value...)
}
