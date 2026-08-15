package mysqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	platformdb "github.com/Zhonghe-zhao/taskpulse/internal/platform/database"
	storeerrors "github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestMySQLTaskStoreCreateAndGetIntegration(t *testing.T) {
	if os.Getenv("TASKPULSE_MYSQL_INTEGRATION") != "1" {
		t.Skip("set TASKPULSE_MYSQL_INTEGRATION=1 to run MySQL store integration tests")
	}

	config, err := platformdb.MySQLConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLConfigFromEnv returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformdb.OpenMySQL(ctx, config)
	if err != nil {
		t.Fatalf("OpenMySQL returned error: %v", err)
	}
	defer db.Close()

	taskStore, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("NewTaskStore returned error: %v", err)
	}
	taskID := fmt.Sprintf("task_mysql_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	task, err := domain.NewTask(
		taskID,
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		3,
		now,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	task.IdempotencyKey = fmt.Sprintf("idem_mysql_%d", time.Now().UnixNano())
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != task.ID || got.Workflow != task.Workflow || got.Status != task.Status {
		t.Fatalf("unexpected stored task: %+v", got)
	}
	if got.IdempotencyKey != task.IdempotencyKey {
		t.Fatalf("expected idempotency key %q, got %q", task.IdempotencyKey, got.IdempotencyKey)
	}
	if !jsonEqual(got.Input, task.Input) {
		t.Fatalf("expected input %s, got %s", task.Input, got.Input)
	}
	if !got.AvailableAt.Equal(task.AvailableAt) ||
		!got.CreatedAt.Equal(task.CreatedAt) ||
		!got.UpdatedAt.Equal(task.UpdatedAt) {
		t.Fatalf(
			"task timestamps changed: available=%s created=%s updated=%s",
			got.AvailableAt,
			got.CreatedAt,
			got.UpdatedAt,
		)
	}
	changedKey := *got
	changedKey.IdempotencyKey = "different-key"
	if err := taskStore.Update(ctx, &changedKey); !errors.Is(err, storeerrors.ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict for changed idempotency key, got %v", err)
	}
	stale := *got
	startedAt := now.Add(time.Second)
	nextAvailableAt := now.Add(time.Minute)
	if err := got.MoveTo(domain.TaskStatusRunning, startedAt); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	got.AvailableAt = nextAvailableAt
	if err := taskStore.Update(ctx, got); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	updated, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get after Update returned error: %v", err)
	}
	if updated.Status != domain.TaskStatusRunning || updated.Version != 1 {
		t.Fatalf("unexpected updated task: %+v", updated)
	}
	if updated.StartedAt == nil || !updated.StartedAt.Equal(startedAt) {
		t.Fatalf("expected started_at %s, got %v", startedAt, updated.StartedAt)
	}
	if !updated.AvailableAt.Equal(nextAvailableAt) {
		t.Fatalf("expected available_at %s, got %s", nextAvailableAt, updated.AvailableAt)
	}

	stale.Progress = 50
	if err := taskStore.Update(ctx, &stale); !errors.Is(err, storeerrors.ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict for stale update, got %v", err)
	}

	if err := taskStore.Create(ctx, task); !errors.Is(err, storeerrors.ErrTaskAlreadyExists) {
		t.Fatalf("expected ErrTaskAlreadyExists, got %v", err)
	}
}

func TestMySQLTaskStoreGetMissingIntegration(t *testing.T) {
	if os.Getenv("TASKPULSE_MYSQL_INTEGRATION") != "1" {
		t.Skip("set TASKPULSE_MYSQL_INTEGRATION=1 to run MySQL store integration tests")
	}

	config, err := platformdb.MySQLConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLConfigFromEnv returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformdb.OpenMySQL(ctx, config)
	if err != nil {
		t.Fatalf("OpenMySQL returned error: %v", err)
	}
	defer db.Close()
	taskStore, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("NewTaskStore returned error: %v", err)
	}

	_, err = taskStore.Get(ctx, "task_that_does_not_exist")
	if !errors.Is(err, storeerrors.ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestMySQLTaskStoreClaimsTaskOnlyOnceIntegration(t *testing.T) {
	if os.Getenv("TASKPULSE_MYSQL_INTEGRATION") != "1" {
		t.Skip("set TASKPULSE_MYSQL_INTEGRATION=1 to run MySQL store integration tests")
	}

	config, err := platformdb.MySQLConfigFromEnv()
	if err != nil {
		t.Fatalf("MySQLConfigFromEnv returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformdb.OpenMySQL(ctx, config)
	if err != nil {
		t.Fatalf("OpenMySQL returned error: %v", err)
	}
	defer db.Close()

	taskStore, err := NewTaskStore(db)
	if err != nil {
		t.Fatalf("NewTaskStore returned error: %v", err)
	}
	if err := cleanupActiveTasks(ctx, db); err != nil {
		t.Fatalf("cleanupActiveTasks returned error: %v", err)
	}
	taskID := fmt.Sprintf("task_mysql_claim_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM tasks WHERE id = ?", taskID)
	})

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	task, err := domain.NewTask(
		taskID,
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		3,
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const claimers = 8
	type claimResult struct {
		task *domain.Task
		err  error
	}
	results := make(chan claimResult, claimers)
	var wg sync.WaitGroup
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		workerID := fmt.Sprintf("worker_%d", i)
		go func() {
			defer wg.Done()
			claimed, claimErr := taskStore.ClaimNext(ctx, storeerrors.ClaimOptions{
				Now:           createdAt.Add(time.Second),
				LeaseDuration: time.Second,
				WorkerID:      workerID,
			})
			results <- claimResult{task: claimed, err: claimErr}
		}()
	}
	wg.Wait()
	close(results)

	var claimedTask *domain.Task
	successes := 0
	for result := range results {
		if result.err == nil {
			if result.task == nil || result.task.ID != taskID {
				t.Fatalf("ClaimNext returned unexpected task: %+v", result.task)
			}
			successes++
			claimedTask = result.task
			continue
		}
		if !errors.Is(result.err, storeerrors.ErrNoTaskAvailable) {
			t.Fatalf("unexpected ClaimNext error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful claim of %s, got %d", taskID, successes)
	}
	if claimedTask == nil || claimedTask.Status != domain.TaskStatusRunning {
		t.Fatalf("unexpected claimed task: %+v", claimedTask)
	}
	if claimedTask.Version != 1 {
		t.Fatalf("expected claimed task version 1, got %d", claimedTask.Version)
	}
	if claimedTask.LeaseOwner == "" || claimedTask.LeaseExpiresAt == nil {
		t.Fatalf("expected claimed task lease, got %+v", claimedTask)
	}

	renewedAt := createdAt.Add(1200 * time.Millisecond)
	if err := taskStore.RenewLease(ctx, storeerrors.RenewLeaseOptions{
		TaskID:        taskID,
		WorkerID:      claimedTask.LeaseOwner,
		Now:           renewedAt,
		LeaseDuration: time.Second,
	}); err != nil {
		t.Fatalf("RenewLease returned error: %v", err)
	}
	if err := taskStore.RenewLease(ctx, storeerrors.RenewLeaseOptions{
		TaskID:        taskID,
		WorkerID:      "worker_without_lease",
		Now:           renewedAt,
		LeaseDuration: time.Second,
	}); !errors.Is(err, storeerrors.ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for another worker, got %v", err)
	}

	stored, err := taskStore.Get(ctx, taskID)
	if err != nil {
		t.Fatalf("Get claimed task returned error: %v", err)
	}
	if stored.Status != domain.TaskStatusRunning || stored.Version != 1 {
		t.Fatalf("unexpected stored claimed task: %+v", stored)
	}
	expectedLeaseExpiry := renewedAt.Add(time.Second)
	if stored.LeaseExpiresAt == nil || !stored.LeaseExpiresAt.Equal(expectedLeaseExpiry) {
		t.Fatalf("expected renewed lease expiry %s, got %v", expectedLeaseExpiry, stored.LeaseExpiresAt)
	}

	recovered, err := taskStore.ClaimNext(ctx, storeerrors.ClaimOptions{
		WorkerID:      "recovery_worker",
		Now:           expectedLeaseExpiry,
		LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("recovery ClaimNext returned error: %v", err)
	}
	if recovered.ID != taskID ||
		recovered.LeaseOwner != "recovery_worker" ||
		recovered.RetryCount != 1 ||
		recovered.Version != 2 {
		t.Fatalf("unexpected recovered task: %+v", recovered)
	}

	if err := claimedTask.MoveTo(domain.TaskStatusSucceeded, expectedLeaseExpiry.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo on stale task returned error: %v", err)
	}
	if err := taskStore.Update(ctx, claimedTask); !errors.Is(err, storeerrors.ErrTaskConflict) {
		t.Fatalf("expected stale worker update conflict, got %v", err)
	}

	current := recovered
	for retry := 2; retry <= task.MaxRetries; retry++ {
		current, err = taskStore.ClaimNext(ctx, storeerrors.ClaimOptions{
			WorkerID:      fmt.Sprintf("recovery_worker_%d", retry),
			Now:           *current.LeaseExpiresAt,
			LeaseDuration: time.Second,
		})
		if err != nil {
			t.Fatalf("recovery %d ClaimNext returned error: %v", retry, err)
		}
		if current.RetryCount != retry {
			t.Fatalf("expected retry count %d, got %d", retry, current.RetryCount)
		}
	}

	failed, err := taskStore.FailNextExpired(ctx, *current.LeaseExpiresAt)
	if err != nil {
		t.Fatalf("FailNextExpired returned error: %v", err)
	}
	if failed.Status != domain.TaskStatusFailed ||
		failed.RetryCount != task.MaxRetries ||
		failed.Version != current.Version+1 ||
		failed.FinishedAt == nil ||
		failed.LeaseOwner != "" ||
		failed.LeaseExpiresAt != nil {
		t.Fatalf("unexpected failed expired task: %+v", failed)
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}
	leftBytes, err := json.Marshal(leftValue)
	if err != nil {
		return false
	}
	rightBytes, err := json.Marshal(rightValue)
	if err != nil {
		return false
	}
	return string(leftBytes) == string(rightBytes)
}
