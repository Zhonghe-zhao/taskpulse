package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

type recordingWorkerTaskMetrics struct {
	claimed      []string
	released     []string
	completed    []domain.TaskStatus
	leaseRenewed []string
	leaseLost    []string
}

func (m *recordingWorkerTaskMetrics) RecordTaskClaimed(workflow string) {
	m.claimed = append(m.claimed, workflow)
}

func (m *recordingWorkerTaskMetrics) RecordTaskReleased(workflow string) {
	m.released = append(m.released, workflow)
}

func (m *recordingWorkerTaskMetrics) RecordTaskCompleted(_ string, status domain.TaskStatus, _ time.Duration) {
	m.completed = append(m.completed, status)
}

func (m *recordingWorkerTaskMetrics) RecordTaskRetried(string, string) {}

func (m *recordingWorkerTaskMetrics) RecordLeaseRenewed(workflow string) {
	m.leaseRenewed = append(m.leaseRenewed, workflow)
}

func (m *recordingWorkerTaskMetrics) RecordLeaseLost(workflow string) {
	m.leaseLost = append(m.leaseLost, workflow)
}

func TestWorkerTaskServiceClaimHeartbeatAndComplete(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	workerService := NewWorkerTaskService(taskService.taskStore, taskService.taskCancellationStore.(store.TaskTransitionStore))
	created, err := taskService.CreateTask(ctx, CreateTaskInput{
		Workflow:   "llm_analysis",
		Input:      json.RawMessage(`{"subject":"go"}`),
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID:      "external-worker-1",
		Workflow:      created.Task.Workflow,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	if claimed.ID != created.Task.ID || claimed.Status != domain.TaskStatusRunning {
		t.Fatalf("unexpected claimed task: %+v", claimed)
	}

	heartbeated, err := workerService.HeartbeatTask(ctx, HeartbeatTaskInput{
		TaskID:        claimed.ID,
		WorkerID:      "external-worker-1",
		LeaseToken:    claimed.LeaseToken,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("HeartbeatTask returned error: %v", err)
	}
	if heartbeated.Status != domain.TaskStatusRunning {
		t.Fatalf("unexpected heartbeated task: %+v", heartbeated)
	}

	progressed, err := workerService.ReportProgress(ctx, ReportProgressInput{
		TaskID:     claimed.ID,
		WorkerID:   "external-worker-1",
		LeaseToken: heartbeated.LeaseToken,
		Version:    heartbeated.Version,
		Progress:   50,
		Message:    "calling llm provider",
	})
	if err != nil {
		t.Fatalf("ReportProgress returned error: %v", err)
	}
	if progressed.Progress != 50 || progressed.Version <= heartbeated.Version {
		t.Fatalf("unexpected progressed task: %+v", progressed)
	}

	completed, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID:     claimed.ID,
		WorkerID:   "external-worker-1",
		LeaseToken: progressed.LeaseToken,
		Version:    progressed.Version,
		Output:     json.RawMessage(`{"summary":"ok"}`),
	})
	if err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}
	if completed.Status != domain.TaskStatusSucceeded || completed.Progress != 100 {
		t.Fatalf("unexpected completed task: %+v", completed)
	}
}

func TestWorkerTaskServiceReleaseRequeuesWithoutUsingRetryBudget(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	metrics := &recordingWorkerTaskMetrics{}
	workerService := NewWorkerTaskService(
		taskService.taskStore,
		taskService.taskCancellationStore.(store.TaskTransitionStore),
	).WithMetrics(metrics)
	created, err := taskService.CreateTask(ctx, CreateTaskInput{
		Workflow: "memobridge.semantic_profile", MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID: "worker-a", Workflow: created.Task.Workflow, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	progressed, err := workerService.ReportProgress(ctx, ReportProgressInput{
		TaskID: claimed.ID, WorkerID: "worker-a", LeaseToken: claimed.LeaseToken,
		Version: claimed.Version, Progress: 60, Message: "calling provider",
	})
	if err != nil {
		t.Fatalf("ReportProgress returned error: %v", err)
	}

	released, err := workerService.ReleaseTask(ctx, ReleaseTaskInput{
		TaskID: progressed.ID, WorkerID: "worker-a", LeaseToken: progressed.LeaseToken, Version: progressed.Version,
	})
	if err != nil {
		t.Fatalf("ReleaseTask returned error: %v", err)
	}
	if released.Status != domain.TaskStatusQueued || released.Progress != 0 || released.RetryCount != 0 {
		t.Fatalf("unexpected released task: %+v", released)
	}
	if released.LeaseOwner != "" || released.LeaseExpiresAt != nil {
		t.Fatalf("released task retained lease: %+v", released)
	}
	if released.Version != progressed.Version+1 {
		t.Fatalf("release version = %d, want %d", released.Version, progressed.Version+1)
	}

	replayed, err := workerService.ReleaseTask(ctx, ReleaseTaskInput{
		TaskID: progressed.ID, WorkerID: "worker-a", LeaseToken: progressed.LeaseToken, Version: progressed.Version,
	})
	if err != nil {
		t.Fatalf("replayed ReleaseTask returned error: %v", err)
	}
	if replayed.Version != released.Version || replayed.Status != domain.TaskStatusQueued {
		t.Fatalf("unexpected replayed release: %+v", replayed)
	}
	if len(metrics.released) != 1 || metrics.released[0] != created.Task.Workflow {
		t.Fatalf("release metric mismatch: %+v", metrics.released)
	}
	if _, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID: progressed.ID, WorkerID: "worker-a", LeaseToken: progressed.LeaseToken, Version: progressed.Version,
	}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expected released worker completion to lose lease, got %v", err)
	}

	reclaimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID: "worker-b", Workflow: created.Task.Workflow, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("replacement ClaimTask returned error: %v", err)
	}
	if reclaimed.ID != created.Task.ID || reclaimed.LeaseToken == progressed.LeaseToken || reclaimed.RetryCount != 0 {
		t.Fatalf("unexpected replacement claim: %+v", reclaimed)
	}
	events, err := taskService.ListTaskEvents(ctx, created.Task.ID)
	if err != nil {
		t.Fatalf("ListTaskEvents returned error: %v", err)
	}
	if events[len(events)-2].Type != domain.EventTaskReleased {
		t.Fatalf("expected release event before replacement claim, got %s", events[len(events)-2].Type)
	}
}

func TestWorkerTaskServiceRejectsStaleCompletion(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	workerService := NewWorkerTaskService(taskService.taskStore, taskService.taskCancellationStore.(store.TaskTransitionStore))
	created, err := taskService.CreateTask(ctx, CreateTaskInput{Workflow: "llm_analysis"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID:      "external-worker-1",
		Workflow:      created.Task.Workflow,
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	progressed, err := workerService.ReportProgress(ctx, ReportProgressInput{
		TaskID:     claimed.ID,
		WorkerID:   "external-worker-1",
		LeaseToken: claimed.LeaseToken,
		Version:    claimed.Version,
		Progress:   10,
	})
	if err != nil {
		t.Fatalf("ReportProgress returned error: %v", err)
	}
	if _, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID:     created.Task.ID,
		WorkerID:   "external-worker-1",
		LeaseToken: claimed.LeaseToken,
		Version:    progressed.Version,
	}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expected stale completion lease loss, got %v", err)
	}
}

func TestWorkerTaskServiceRejectsMissingLeaseToken(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	workerService := NewWorkerTaskService(taskService.taskStore, taskService.taskCancellationStore.(store.TaskTransitionStore))
	created, err := taskService.CreateTask(ctx, CreateTaskInput{Workflow: "llm_analysis"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID: "external-worker-1", Workflow: created.Task.Workflow, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	if _, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID: created.Task.ID, WorkerID: "external-worker-1", Version: claimed.Version,
	}); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expected missing lease token to be rejected, got %v", err)
	}
}

func TestWorkerTaskServiceLeaseTokenAndCompleteReplay(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	workerService := NewWorkerTaskService(taskService.taskStore, taskService.taskCancellationStore.(store.TaskTransitionStore))
	created, err := taskService.CreateTask(ctx, CreateTaskInput{Workflow: "memobridge.semantic_profile"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID:      "memobridge-worker-1",
		Workflow:      "memobridge.semantic_profile",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	if claimed.TaskID != created.Task.ID || claimed.LeaseToken == "" || claimed.LeaseUntil == nil {
		t.Fatalf("claim did not return external lease fields: %+v", claimed)
	}

	completed, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID:     claimed.ID,
		WorkerID:   "memobridge-worker-1",
		LeaseToken: claimed.LeaseToken,
		ResultRef:  json.RawMessage(`{"source_item_id":11778,"content_hash":"sha256:test"}`),
	})
	if err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}
	replayed, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID:     claimed.ID,
		WorkerID:   "memobridge-worker-1",
		LeaseToken: claimed.LeaseToken,
		ResultRef:  json.RawMessage(`{"content_hash":"sha256:test","source_item_id":11778}`),
	})
	if err != nil {
		t.Fatalf("duplicate CompleteTask returned error: %v", err)
	}
	if replayed.ID != completed.ID || replayed.Status != domain.TaskStatusSucceeded {
		t.Fatalf("unexpected replayed task: %+v", replayed)
	}
	if string(replayed.Result) != `{"source_item_id":11778,"content_hash":"sha256:test"}` {
		t.Fatalf("result_ref was not persisted as task reference: %s", replayed.Result)
	}
	if _, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID:     claimed.ID,
		WorkerID:   "memobridge-worker-1",
		LeaseToken: claimed.LeaseToken,
		ResultRef:  json.RawMessage(`{"different":true}`),
	}); !errors.Is(err, store.ErrTaskConflict) {
		t.Fatalf("expected conflicting Complete replay to be rejected, got %v", err)
	}
}

func TestWorkerTaskServiceFailReplayRequiresSameFailure(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	workerService := NewWorkerTaskService(taskService.taskStore, taskService.taskCancellationStore.(store.TaskTransitionStore))
	created, err := taskService.CreateTask(ctx, CreateTaskInput{Workflow: "memobridge.semantic_profile"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID: "memobridge-worker-1", Workflow: created.Task.Workflow, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	request := FailTaskInput{
		TaskID: claimed.ID, WorkerID: "memobridge-worker-1", LeaseToken: claimed.LeaseToken,
		ErrorCode: "source_not_found", ErrorMessage: "source item does not exist", Retryable: false,
	}
	failed, err := workerService.FailTask(ctx, request)
	if err != nil {
		t.Fatalf("FailTask returned error: %v", err)
	}
	replayed, err := workerService.FailTask(ctx, request)
	if err != nil {
		t.Fatalf("replayed FailTask returned error: %v", err)
	}
	if replayed.ID != failed.ID || replayed.Status != domain.TaskStatusFailed {
		t.Fatalf("unexpected replayed failed task: %+v", replayed)
	}

	conflicting := request
	conflicting.ErrorCode = "invalid_model_output"
	conflicting.ErrorMessage = "model response did not match schema"
	if _, err := workerService.FailTask(ctx, conflicting); !errors.Is(err, store.ErrTaskConflict) {
		t.Fatalf("expected conflicting Fail replay to be rejected, got %v", err)
	}
}

func TestWorkerTaskServiceRecordsExternalWorkerMetrics(t *testing.T) {
	ctx := context.Background()
	taskService := newMemoryTaskService()
	metrics := &recordingWorkerTaskMetrics{}
	workerService := NewWorkerTaskService(
		taskService.taskStore,
		taskService.taskCancellationStore.(store.TaskTransitionStore),
	).WithMetrics(metrics)
	created, err := taskService.CreateTask(ctx, CreateTaskInput{Workflow: "memobridge.semantic_profile"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	claimed, err := workerService.ClaimTask(ctx, ClaimTaskInput{
		WorkerID: "memobridge-worker-1", Workflow: created.Task.Workflow, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("ClaimTask returned error: %v", err)
	}
	heartbeated, err := workerService.HeartbeatTask(ctx, HeartbeatTaskInput{
		TaskID: claimed.ID, WorkerID: "memobridge-worker-1", LeaseToken: claimed.LeaseToken, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("HeartbeatTask returned error: %v", err)
	}
	if _, err := workerService.CompleteTask(ctx, CompleteTaskInput{
		TaskID: claimed.ID, WorkerID: "memobridge-worker-1", LeaseToken: heartbeated.LeaseToken,
		ResultRef: json.RawMessage(`{"source_item_id":11778}`),
	}); err != nil {
		t.Fatalf("CompleteTask returned error: %v", err)
	}
	if len(metrics.claimed) != 1 || metrics.claimed[0] != created.Task.Workflow {
		t.Fatalf("claim metric mismatch: %+v", metrics.claimed)
	}
	if len(metrics.leaseRenewed) != 1 || metrics.leaseRenewed[0] != created.Task.Workflow {
		t.Fatalf("lease renewal metric mismatch: %+v", metrics.leaseRenewed)
	}
	if len(metrics.completed) != 1 || metrics.completed[0] != domain.TaskStatusSucceeded {
		t.Fatalf("completion metric mismatch: %+v", metrics.completed)
	}
}
