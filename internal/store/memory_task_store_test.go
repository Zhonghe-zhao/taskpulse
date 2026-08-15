package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

func TestMemoryTaskStoreCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	got, err := store.Get(ctx, "task_1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.ID != task.ID {
		t.Fatalf("expected task id %s, got %s", task.ID, got.ID)
	}
	if got.Workflow != task.Workflow {
		t.Fatalf("expected workflow %s, got %s", task.Workflow, got.Workflow)
	}
}

func TestMemoryTaskStoreRejectsDuplicateTaskID(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := store.Create(ctx, task); !errors.Is(err, ErrTaskAlreadyExists) {
		t.Fatalf("expected ErrTaskAlreadyExists, got %v", err)
	}
}

func TestMemoryTaskStoreReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()

	if _, err := store.Get(ctx, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestMemoryTaskStoreUpdate(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	if err := task.MoveTo(domain.TaskStatusRunning, now); err != nil {
		t.Fatalf("MoveTo returned error: %v", err)
	}
	if err := store.Update(ctx, task); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	got, err := store.Get(ctx, "task_1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != domain.TaskStatusRunning {
		t.Fatalf("expected running status, got %s", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("expected version 1 after update, got %d", got.Version)
	}
}

func TestMemoryTaskStoreRejectsStaleUpdate(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	first, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("first Get returned error: %v", err)
	}
	stale, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}

	first.Progress = 50
	if err := taskStore.Update(ctx, first); err != nil {
		t.Fatalf("first Update returned error: %v", err)
	}
	stale.Progress = 80
	if err := taskStore.Update(ctx, stale); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict, got %v", err)
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("final Get returned error: %v", err)
	}
	if got.Progress != 50 || got.Version != 1 {
		t.Fatalf("stale update changed stored task: %+v", got)
	}
}

func TestMemoryTaskStoreRejectsIdempotencyKeyChange(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	task.IdempotencyKey = "request-1"
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	stored, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	stored.IdempotencyKey = "request-2"
	if err := taskStore.Update(ctx, stored); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected ErrTaskConflict, got %v", err)
	}
}

func TestMemoryTaskStoreAllowsSameIdempotencyKeyAcrossWorkflows(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	first := newTestTask(t, "task_1")
	first.IdempotencyKey = "same-key"
	second := newTestTask(t, "task_2")
	second.Workflow = "memobridge.semantic_profile"
	second.IdempotencyKey = first.IdempotencyKey
	if err := taskStore.Create(ctx, first); err != nil {
		t.Fatalf("first Create returned error: %v", err)
	}
	if err := taskStore.Create(ctx, second); err != nil {
		t.Fatalf("same key in another workflow should be allowed: %v", err)
	}
}

func TestMemoryTaskStoreRejectsUpdateForMissingTask(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Update(ctx, task); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestMemoryTaskStoreCopiesCreatedTask(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	task.Status = domain.TaskStatusSucceeded
	task.Input[0] = '['

	got, err := store.Get(ctx, "task_1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.Status != domain.TaskStatusQueued {
		t.Fatalf("expected stored status to stay queued, got %s", got.Status)
	}
	if !json.Valid(got.Input) || got.Input[0] != '{' {
		t.Fatalf("expected stored input to be an independent JSON copy, got %s", string(got.Input))
	}
}

func TestMemoryTaskStoreReturnsTaskCopy(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	got, err := store.Get(ctx, "task_1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	got.Status = domain.TaskStatusFailed
	got.Input[0] = '['

	again, err := store.Get(ctx, "task_1")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if again.Status != domain.TaskStatusQueued {
		t.Fatalf("expected stored status to stay queued, got %s", again.Status)
	}
	if !json.Valid(again.Input) || again.Input[0] != '{' {
		t.Fatalf("expected returned input to be independent from store data, got %s", string(again.Input))
	}
}

func TestMemoryTaskStoreReturnsIndependentTimePointers(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	startedAt := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	finishedAt := time.Date(2026, 6, 16, 12, 2, 0, 0, time.UTC)
	if err := task.MoveTo(domain.TaskStatusRunning, startedAt); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	if err := task.MoveTo(domain.TaskStatusSucceeded, finishedAt); err != nil {
		t.Fatalf("MoveTo succeeded returned error: %v", err)
	}
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	got, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	changed := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	*got.StartedAt = changed
	*got.FinishedAt = changed

	again, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if !again.StartedAt.Equal(startedAt) {
		t.Fatalf("stored started_at changed through returned copy: got %s", again.StartedAt)
	}
	if !again.FinishedAt.Equal(finishedAt) {
		t.Fatalf("stored finished_at changed through returned copy: got %s", again.FinishedAt)
	}
}

func TestMemoryTaskStoreConcurrentCreateAndGet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()

			id := fmt.Sprintf("task_%d", i)
			task := newTestTask(t, id)
			if err := store.Create(ctx, task); err != nil {
				t.Errorf("Create(%s) returned error: %v", id, err)
				return
			}
			if _, err := store.Get(ctx, id); err != nil {
				t.Errorf("Get(%s) returned error: %v", id, err)
			}
		}()
	}

	wg.Wait()
}

func TestMemoryTaskStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")

	if err := store.Create(ctx, task); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestMemoryTaskStoreClaimNext(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	if err := taskStore.Create(ctx, newTestTask(t, "task_1")); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	now := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	claimed, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           now,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	if claimed.Status != domain.TaskStatusRunning {
		t.Fatalf("expected running status, got %s", claimed.Status)
	}
	if claimed.StartedAt == nil || !claimed.StartedAt.Equal(now) {
		t.Fatalf("expected started_at %s, got %v", now, claimed.StartedAt)
	}
	if claimed.LeaseOwner != "worker_1" {
		t.Fatalf("expected lease owner worker_1, got %q", claimed.LeaseOwner)
	}
	expectedLeaseExpiry := now.Add(time.Minute)
	if claimed.LeaseExpiresAt == nil || !claimed.LeaseExpiresAt.Equal(expectedLeaseExpiry) {
		t.Fatalf("expected lease expiry %s, got %v", expectedLeaseExpiry, claimed.LeaseExpiresAt)
	}
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           now,
		LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected ErrNoTaskAvailable, got %v", err)
	}
}

func TestMemoryTaskStoreWaitsUntilTaskIsAvailable(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	task.AvailableAt = task.CreatedAt.Add(time.Hour)
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.AvailableAt.Add(-time.Nanosecond),
		LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected task not to be available yet, got %v", err)
	}

	claimed, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           task.AvailableAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext at available time returned error: %v", err)
	}
	if claimed.ID != task.ID {
		t.Fatalf("expected task %s, got %s", task.ID, claimed.ID)
	}
}

func TestMemoryTaskStoreClaimFiltersWorkflow(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	other := newTestTask(t, "task_other")
	other.Workflow = "other.workflow"
	target := newTestTask(t, "task_target")
	target.Workflow = "memobridge.semantic_profile"
	if err := taskStore.Create(ctx, other); err != nil {
		t.Fatalf("create other task returned error: %v", err)
	}
	if err := taskStore.Create(ctx, target); err != nil {
		t.Fatalf("create target task returned error: %v", err)
	}

	claimed, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "memobridge-worker-1",
		Workflow:      "memobridge.semantic_profile",
		Now:           target.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}
	if claimed.ID != target.ID || claimed.Workflow != target.Workflow {
		t.Fatalf("expected target workflow task, got id=%s workflow=%s", claimed.ID, claimed.Workflow)
	}

	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "memobridge-worker-1",
		Workflow:      "memobridge.semantic_profile",
		Now:           target.CreatedAt.Add(time.Second),
		LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected no matching task after claim, got %v", err)
	}
}

func TestMemoryTaskStoreRejectsInvalidClaim(t *testing.T) {
	taskStore := NewMemoryTaskStore()
	_, err := taskStore.ClaimNext(context.Background(), ClaimOptions{})
	if !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("expected ErrInvalidClaim, got %v", err)
	}
}

func TestMemoryTaskStoreRenewsLeaseForOwner(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	claimed, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	renewedAt := claimedAt.Add(20 * time.Second)
	if err := taskStore.RenewLease(ctx, RenewLeaseOptions{
		TaskID:        task.ID,
		WorkerID:      "worker_1",
		Now:           renewedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("RenewLease returned error: %v", err)
	}

	renewed, err := taskStore.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	expectedExpiry := renewedAt.Add(time.Minute)
	if renewed.LeaseExpiresAt == nil || !renewed.LeaseExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("expected lease expiry %s, got %v", expectedExpiry, renewed.LeaseExpiresAt)
	}
	if renewed.Version != claimed.Version {
		t.Fatalf("lease renewal changed task version: before=%d after=%d", claimed.Version, renewed.Version)
	}
}

func TestMemoryTaskStoreRejectsLeaseRenewalAfterOwnershipLost(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("ClaimNext returned error: %v", err)
	}

	err := taskStore.RenewLease(ctx, RenewLeaseOptions{
		TaskID:        task.ID,
		WorkerID:      "worker_2",
		Now:           claimedAt.Add(20 * time.Second),
		LeaseDuration: time.Minute,
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for another worker, got %v", err)
	}

	err = taskStore.RenewLease(ctx, RenewLeaseOptions{
		TaskID:        task.ID,
		WorkerID:      "worker_1",
		Now:           claimedAt.Add(time.Minute),
		LeaseDuration: time.Minute,
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost for expired lease, got %v", err)
	}
}

func TestMemoryTaskStoreRecoversExpiredTask(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	stale, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("first ClaimNext returned error: %v", err)
	}
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_2",
		Now:           claimedAt.Add(30 * time.Second),
		LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected active lease to prevent recovery, got %v", err)
	}

	recoveredAt := claimedAt.Add(time.Minute)
	recovered, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_2",
		Now:           recoveredAt,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("recovery ClaimNext returned error: %v", err)
	}
	if recovered.ID != task.ID ||
		recovered.LeaseOwner != "worker_2" ||
		recovered.RetryCount != 1 ||
		recovered.Version != stale.Version+1 {
		t.Fatalf("unexpected recovered task: %+v", recovered)
	}

	if err := stale.MoveTo(domain.TaskStatusSucceeded, recoveredAt.Add(time.Second)); err != nil {
		t.Fatalf("MoveTo on stale task returned error: %v", err)
	}
	if err := taskStore.Update(ctx, stale); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("expected stale worker update conflict, got %v", err)
	}
}

func TestMemoryTaskStoreDoesNotRecoverAfterRetryBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	task := newTestTask(t, "task_1")
	task.MaxRetries = 0
	if err := taskStore.Create(ctx, task); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	claimedAt := time.Date(2026, 6, 16, 12, 1, 0, 0, time.UTC)
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_1",
		Now:           claimedAt,
		LeaseDuration: time.Minute,
	}); err != nil {
		t.Fatalf("first ClaimNext returned error: %v", err)
	}
	if _, err := taskStore.ClaimNext(ctx, ClaimOptions{
		WorkerID:      "worker_2",
		Now:           claimedAt.Add(time.Minute),
		LeaseDuration: time.Minute,
	}); !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected exhausted task not to be recovered, got %v", err)
	}

	failedAt := claimedAt.Add(time.Minute)
	failed, err := taskStore.FailNextExpired(ctx, failedAt)
	if err != nil {
		t.Fatalf("FailNextExpired returned error: %v", err)
	}
	if failed.ID != task.ID ||
		failed.Status != domain.TaskStatusFailed ||
		failed.Version != 2 ||
		failed.FinishedAt == nil ||
		failed.LeaseOwner != "" ||
		failed.LeaseExpiresAt != nil {
		t.Fatalf("unexpected failed expired task: %+v", failed)
	}
	if _, err := taskStore.FailNextExpired(ctx, failedAt); !errors.Is(err, ErrNoExpiredTask) {
		t.Fatalf("expected ErrNoExpiredTask after cleanup, got %v", err)
	}
}

func TestMemoryTaskStoreClaimsTaskOnlyOnceConcurrently(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	if err := taskStore.Create(ctx, newTestTask(t, "task_1")); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	const claimers = 10
	results := make(chan error, claimers)
	var wg sync.WaitGroup
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := taskStore.ClaimNext(ctx, ClaimOptions{
				WorkerID:      fmt.Sprintf("worker_%d", i),
				Now:           time.Now(),
				LeaseDuration: time.Minute,
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrNoTaskAvailable) {
			t.Fatalf("unexpected ClaimNext error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successes)
	}
}

func newTestTask(t *testing.T, id string) *domain.Task {
	t.Helper()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	task, err := domain.NewTask(
		id,
		"url_check",
		json.RawMessage(`{"urls":["https://example.com"]}`),
		3,
		now,
	)
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	return task
}
