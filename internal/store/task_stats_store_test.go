package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

func TestMemoryTaskStoreSnapshotsTaskStats(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	taskStore := NewMemoryTaskStore()

	queued, err := domain.NewTask("task_queued", "llm_analysis", nil, 0, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("NewTask queued returned error: %v", err)
	}
	retrying, err := domain.NewTask("task_retrying", "llm_analysis", nil, 1, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("NewTask retrying returned error: %v", err)
	}
	if err := retrying.MoveTo(domain.TaskStatusRunning, now.Add(-50*time.Second)); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}
	if err := retrying.ScheduleRetry(now.Add(-40*time.Second), now.Add(10*time.Second)); err != nil {
		t.Fatalf("ScheduleRetry returned error: %v", err)
	}
	running, err := domain.NewTask("task_running", "url_check", nil, 0, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("NewTask running returned error: %v", err)
	}
	if err := running.MoveTo(domain.TaskStatusRunning, now.Add(-30*time.Second)); err != nil {
		t.Fatalf("MoveTo running returned error: %v", err)
	}

	for _, task := range []*domain.Task{queued, retrying, running} {
		if err := taskStore.Create(ctx, task); err != nil {
			t.Fatalf("Create %s returned error: %v", task.ID, err)
		}
	}

	snapshot, err := taskStore.SnapshotTaskStats(ctx, now)
	if err != nil {
		t.Fatalf("SnapshotTaskStats returned error: %v", err)
	}
	if snapshot.StatusCounts[domain.TaskStatusQueued] != 1 ||
		snapshot.StatusCounts[domain.TaskStatusRetrying] != 1 ||
		snapshot.StatusCounts[domain.TaskStatusRunning] != 1 {
		t.Fatalf("unexpected status counts: %+v", snapshot.StatusCounts)
	}
	if snapshot.AvailableCounts[domain.TaskStatusQueued] != 1 {
		t.Fatalf("unexpected available counts: %+v", snapshot.AvailableCounts)
	}
	if snapshot.AvailableCounts[domain.TaskStatusRetrying] != 0 {
		t.Fatalf("future retrying task should not be available: %+v", snapshot.AvailableCounts)
	}
	if snapshot.OldestAvailableAge[domain.TaskStatusQueued] != 2*time.Minute {
		t.Fatalf("unexpected oldest queued age: %+v", snapshot.OldestAvailableAge)
	}
}

func TestMemoryTaskStoreSnapshotsFilteredTaskStats(t *testing.T) {
	ctx := context.Background()
	taskStore := NewMemoryTaskStore()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	for index, workflow := range []string{"llm_analysis", "memobridge.semantic_profile", "memobridge.semantic_profile"} {
		task, err := domain.NewTask(fmt.Sprintf("task_%d", index), workflow, nil, 3, now)
		if err != nil {
			t.Fatalf("NewTask returned error: %v", err)
		}
		if err := taskStore.Create(ctx, task); err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
	}

	snapshot, err := taskStore.SnapshotFilteredTaskStats(ctx, now, TaskStatsFilter{
		Workflow: "memobridge.semantic_profile",
		Status:   domain.TaskStatusQueued,
	})
	if err != nil {
		t.Fatalf("SnapshotFilteredTaskStats returned error: %v", err)
	}
	if snapshot.StatusCounts[domain.TaskStatusQueued] != 2 {
		t.Fatalf("expected 2 filtered queued tasks, got %+v", snapshot.StatusCounts)
	}
}
