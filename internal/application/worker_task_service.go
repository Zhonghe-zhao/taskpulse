package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/identity"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

var (
	ErrInvalidWorkerRequest = errors.New("invalid worker request")
	ErrWorkerTaskNotOwned   = errors.New("worker does not own task")
)

type WorkerTaskService struct {
	taskStore       store.TaskStore
	transitionStore store.TaskTransitionStore
	retryScheduler  TaskRetryScheduler
	metrics         WorkerTaskMetrics
	now             func() time.Time
}

// TaskRetryScheduler keeps retry policy outside the worker transport layer.
// Both in-process and external workers can therefore share the same retry semantics.
type TaskRetryScheduler interface {
	Schedule(ctx context.Context, task *domain.Task, code, message string, retryAfter time.Duration) error
}

// WorkerTaskMetrics records operations completed through the public Worker
// protocol. It mirrors the internal worker metrics so external Workers add to
// the same Prometheus series.
type WorkerTaskMetrics interface {
	RecordTaskClaimed(workflow string)
	RecordTaskReleased(workflow string)
	RecordTaskCompleted(workflow string, status domain.TaskStatus, duration time.Duration)
	RecordTaskRetried(workflow string, errorCode string)
	RecordLeaseRenewed(workflow string)
	RecordLeaseLost(workflow string)
}

type noopWorkerTaskMetrics struct{}

func (noopWorkerTaskMetrics) RecordTaskClaimed(string)                                     {}
func (noopWorkerTaskMetrics) RecordTaskReleased(string)                                    {}
func (noopWorkerTaskMetrics) RecordTaskCompleted(string, domain.TaskStatus, time.Duration) {}
func (noopWorkerTaskMetrics) RecordTaskRetried(string, string)                             {}
func (noopWorkerTaskMetrics) RecordLeaseRenewed(string)                                    {}
func (noopWorkerTaskMetrics) RecordLeaseLost(string)                                       {}

type ClaimTaskInput struct {
	WorkerID      string
	Workflow      string
	LeaseDuration time.Duration
}

type HeartbeatTaskInput struct {
	TaskID        string
	WorkerID      string
	LeaseToken    string
	LeaseDuration time.Duration
}

type CompleteTaskInput struct {
	TaskID     string
	WorkerID   string
	Version    uint64
	LeaseToken string
	Output     json.RawMessage
	ResultRef  json.RawMessage
}

type ReportProgressInput struct {
	TaskID     string
	WorkerID   string
	Version    uint64
	LeaseToken string
	Progress   int
	Message    string
}

type FailTaskInput struct {
	TaskID       string
	WorkerID     string
	Version      uint64
	LeaseToken   string
	ErrorCode    string
	ErrorMessage string
	Retryable    bool
	RetryAfter   time.Duration
}

type ReleaseTaskInput struct {
	TaskID     string
	WorkerID   string
	Version    uint64
	LeaseToken string
}

func NewWorkerTaskService(
	taskStore store.TaskStore,
	transitionStore store.TaskTransitionStore,
) *WorkerTaskService {
	return &WorkerTaskService{
		taskStore:       taskStore,
		transitionStore: transitionStore,
		metrics:         noopWorkerTaskMetrics{},
		now:             time.Now,
	}
}

func (s *WorkerTaskService) WithRetryScheduler(scheduler TaskRetryScheduler) *WorkerTaskService {
	s.retryScheduler = scheduler
	return s
}

func (s *WorkerTaskService) WithMetrics(metrics WorkerTaskMetrics) *WorkerTaskService {
	if metrics != nil {
		s.metrics = metrics
	}
	return s
}

func (s *WorkerTaskService) ClaimTask(
	ctx context.Context,
	input ClaimTaskInput,
) (*domain.Task, error) {
	if strings.TrimSpace(input.WorkerID) == "" || strings.TrimSpace(input.Workflow) == "" || input.LeaseDuration <= 0 {
		return nil, ErrInvalidWorkerRequest
	}
	task, err := s.transitionStore.ClaimNextWithEvent(
		ctx,
		store.ClaimOptions{
			WorkerID:      input.WorkerID,
			Workflow:      strings.TrimSpace(input.Workflow),
			Now:           s.now(),
			LeaseDuration: input.LeaseDuration,
		},
		identity.New("event"),
	)
	if err != nil {
		return nil, err
	}
	s.metrics.RecordTaskClaimed(task.Workflow)
	return withLeaseToken(task), nil
}

func (s *WorkerTaskService) HeartbeatTask(
	ctx context.Context,
	input HeartbeatTaskInput,
) (*domain.Task, error) {
	if input.TaskID == "" || strings.TrimSpace(input.WorkerID) == "" || input.LeaseDuration <= 0 {
		return nil, ErrInvalidWorkerRequest
	}
	now := s.now()
	if err := s.validateLeaseToken(ctx, input.TaskID, input.WorkerID, input.LeaseToken); err != nil {
		s.recordLeaseLoss(ctx, input.TaskID, err)
		return nil, err
	}
	if err := s.taskStore.RenewLease(ctx, store.RenewLeaseOptions{
		TaskID:        input.TaskID,
		WorkerID:      input.WorkerID,
		Now:           now,
		LeaseDuration: input.LeaseDuration,
	}); err != nil {
		s.recordLeaseLoss(ctx, input.TaskID, err)
		return nil, err
	}
	task, err := s.taskStore.Get(ctx, input.TaskID)
	if err != nil {
		return nil, err
	}
	s.metrics.RecordLeaseRenewed(task.Workflow)
	return withLeaseToken(task), nil
}

func (s *WorkerTaskService) CompleteTask(
	ctx context.Context,
	input CompleteTaskInput,
) (*domain.Task, error) {
	result := input.ResultRef
	if len(result) == 0 {
		result = input.Output
	}
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	if task, replayed, err := s.replayWorkerOperation(ctx, input.TaskID, input.WorkerID, input.LeaseToken, domain.TaskStatusSucceeded); err != nil {
		return nil, err
	} else if replayed {
		if !jsonSemanticallyEqual(task.Result, result) {
			return nil, store.ErrTaskConflict
		}
		return task, nil
	}
	version, err := s.resolveLeaseVersion(ctx, input.TaskID, input.WorkerID, input.Version, input.LeaseToken)
	if err != nil {
		return nil, err
	}
	task, err := s.prepareRunningTask(ctx, input.TaskID, input.WorkerID, version)
	if err != nil {
		return nil, err
	}
	task.Result = append(json.RawMessage(nil), result...)
	task.Progress = 100
	now := s.now()
	if err := task.MoveTo(domain.TaskStatusSucceeded, now); err != nil {
		return nil, err
	}
	event, err := domain.NewTaskEvent(
		identity.New("event"),
		task.ID,
		domain.EventTaskSucceeded,
		"task succeeded by external worker",
		mustMarshalEventPayload(map[string]any{"worker_id": input.WorkerID}),
		task.Progress,
		now,
	)
	if err != nil {
		return nil, err
	}
	if err := s.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return nil, err
	}
	s.metrics.RecordTaskCompleted(task.Workflow, domain.TaskStatusSucceeded, taskElapsed(task, now))
	return task, nil
}

func (s *WorkerTaskService) ReportProgress(
	ctx context.Context,
	input ReportProgressInput,
) (*domain.Task, error) {
	if input.Progress < 0 || input.Progress >= 100 {
		return nil, fmt.Errorf("%w: progress must be between 0 and 99", ErrInvalidWorkerRequest)
	}
	version, err := s.resolveLeaseVersion(ctx, input.TaskID, input.WorkerID, input.Version, input.LeaseToken)
	if err != nil {
		return nil, err
	}
	task, err := s.prepareRunningTask(ctx, input.TaskID, input.WorkerID, version)
	if err != nil {
		return nil, err
	}
	task.Progress = input.Progress
	now := s.now()
	task.UpdatedAt = now
	message := strings.TrimSpace(input.Message)
	if message == "" {
		message = "task progress updated"
	}
	payload, err := json.Marshal(map[string]any{
		"progress":  input.Progress,
		"message":   message,
		"worker_id": input.WorkerID,
	})
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskEvent(
		identity.New("event"),
		task.ID,
		domain.EventTaskProgress,
		message,
		payload,
		input.Progress,
		now,
	)
	if err != nil {
		return nil, err
	}
	if err := s.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return nil, err
	}
	updated, err := s.taskStore.Get(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	return withLeaseToken(updated), nil
}

func (s *WorkerTaskService) FailTask(
	ctx context.Context,
	input FailTaskInput,
) (*domain.Task, error) {
	if task, replayed, err := s.replayWorkerOperation(ctx, input.TaskID, input.WorkerID, input.LeaseToken, domain.TaskStatusRetrying, domain.TaskStatusFailed); err != nil {
		return nil, err
	} else if replayed {
		if task.ErrorMessage != input.ErrorCode+": "+input.ErrorMessage {
			return nil, store.ErrTaskConflict
		}
		return task, nil
	}
	version, err := s.resolveLeaseVersion(ctx, input.TaskID, input.WorkerID, input.Version, input.LeaseToken)
	if err != nil {
		return nil, err
	}
	task, err := s.prepareRunningTask(ctx, input.TaskID, input.WorkerID, version)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ErrorCode) == "" || strings.TrimSpace(input.ErrorMessage) == "" {
		return nil, fmt.Errorf("%w: error_code and error_message are required", ErrInvalidWorkerRequest)
	}
	if input.RetryAfter < 0 {
		return nil, fmt.Errorf("%w: retry_after cannot be negative", ErrInvalidWorkerRequest)
	}
	if input.Retryable && s.retryScheduler != nil {
		err := s.retryScheduler.Schedule(
			ctx,
			task,
			input.ErrorCode,
			input.ErrorMessage,
			input.RetryAfter,
		)
		if err == nil {
			s.metrics.RecordTaskRetried(task.Workflow, input.ErrorCode)
			return s.taskStore.Get(ctx, task.ID)
		}
		if !errors.Is(err, domain.ErrRetryBudgetExhausted) {
			return nil, err
		}
	}
	task.ErrorMessage = input.ErrorCode + ": " + input.ErrorMessage
	now := s.now()
	if err := task.MoveTo(domain.TaskStatusFailed, now); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"error_code": input.ErrorCode,
		"retryable":  input.Retryable,
		"worker_id":  input.WorkerID,
	})
	if err != nil {
		return nil, err
	}
	event, err := domain.NewTaskEvent(
		identity.New("event"),
		task.ID,
		domain.EventTaskFailed,
		"task failed by external worker",
		payload,
		task.Progress,
		now,
	)
	if err != nil {
		return nil, err
	}
	if err := s.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return nil, err
	}
	s.metrics.RecordTaskCompleted(task.Workflow, domain.TaskStatusFailed, taskElapsed(task, now))
	return task, nil
}

func jsonSemanticallyEqual(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, leftErr := json.Marshal(leftValue)
	rightJSON, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// ReleaseTask immediately makes a running task available after its current
// Worker has stopped execution during graceful shutdown. The same fencing
// token that protects Complete and Fail prevents an old Worker from releasing
// or completing a task claimed by its replacement.
func (s *WorkerTaskService) ReleaseTask(
	ctx context.Context,
	input ReleaseTaskInput,
) (*domain.Task, error) {
	if task, replayed, err := s.replayWorkerOperation(ctx, input.TaskID, input.WorkerID, input.LeaseToken, domain.TaskStatusQueued); err != nil {
		return nil, err
	} else if replayed {
		return task, nil
	}
	version, err := s.resolveLeaseVersion(ctx, input.TaskID, input.WorkerID, input.Version, input.LeaseToken)
	if err != nil {
		return nil, err
	}
	task, err := s.prepareRunningTask(ctx, input.TaskID, input.WorkerID, version)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := task.RequeueForRelease(now); err != nil {
		return nil, err
	}
	event, err := domain.NewTaskReleasedEvent(identity.New("event"), task, now)
	if err != nil {
		return nil, err
	}
	if err := s.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return nil, err
	}
	s.metrics.RecordTaskReleased(task.Workflow)
	return s.taskStore.Get(ctx, task.ID)
}

func (s *WorkerTaskService) recordLeaseLoss(ctx context.Context, taskID string, err error) {
	if !errors.Is(err, store.ErrLeaseLost) {
		return
	}
	task, getErr := s.taskStore.Get(ctx, taskID)
	if getErr == nil {
		s.metrics.RecordLeaseLost(task.Workflow)
	}
}

func taskElapsed(task *domain.Task, now time.Time) time.Duration {
	if task == nil {
		return 0
	}
	startedAt := task.CreatedAt
	if task.StartedAt != nil {
		startedAt = *task.StartedAt
	}
	if now.Before(startedAt) {
		return 0
	}
	return now.Sub(startedAt)
}

func mustMarshalEventPayload(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("{}")
	}
	return payload
}

type leaseTokenPayload struct {
	TaskID   string `json:"task_id"`
	WorkerID string `json:"worker_id"`
	Version  uint64 `json:"version"`
}

func withLeaseToken(task *domain.Task) *domain.Task {
	if task == nil || task.LeaseOwner == "" {
		return task
	}
	copy := *task
	copy.LeaseToken = makeLeaseToken(&copy)
	copy.TaskID = copy.ID
	copy.LeaseUntil = copy.LeaseExpiresAt
	return &copy
}

func makeLeaseToken(task *domain.Task) string {
	payload, _ := json.Marshal(leaseTokenPayload{
		TaskID: task.ID, WorkerID: task.LeaseOwner, Version: task.Version,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func parseLeaseToken(raw string) (leaseTokenPayload, error) {
	var payload leaseTokenPayload
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(decoded, &payload) != nil ||
		payload.TaskID == "" || payload.WorkerID == "" || payload.Version == 0 {
		return leaseTokenPayload{}, store.ErrLeaseLost
	}
	return payload, nil
}

func (s *WorkerTaskService) validateLeaseToken(ctx context.Context, taskID, workerID, token string) error {
	if strings.TrimSpace(token) == "" {
		return store.ErrLeaseLost
	}
	payload, err := parseLeaseToken(token)
	if err != nil || payload.TaskID != taskID || payload.WorkerID != workerID {
		return store.ErrLeaseLost
	}
	task, err := s.taskStore.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Version != payload.Version || task.LeaseOwner != workerID {
		return store.ErrLeaseLost
	}
	return nil
}

func (s *WorkerTaskService) resolveLeaseVersion(ctx context.Context, taskID, workerID string, version uint64, token string) (uint64, error) {
	if strings.TrimSpace(token) == "" {
		return 0, store.ErrLeaseLost
	}
	payload, err := parseLeaseToken(token)
	if err != nil || payload.TaskID != taskID || payload.WorkerID != workerID {
		return 0, store.ErrLeaseLost
	}
	if err := s.validateLeaseToken(ctx, taskID, workerID, token); err != nil {
		return 0, err
	}
	return payload.Version, nil
}

// replayWorkerOperation recognizes the state left by a request that reached
// the database but whose HTTP response was lost. The version increment made
// by the original transition is the fencing evidence that this token was
// already consumed. No event or task update is performed on replay.
func (s *WorkerTaskService) replayWorkerOperation(
	ctx context.Context,
	taskID string,
	workerID string,
	token string,
	statuses ...domain.TaskStatus,
) (*domain.Task, bool, error) {
	if strings.TrimSpace(token) == "" {
		return nil, false, store.ErrLeaseLost
	}
	payload, err := parseLeaseToken(token)
	if err != nil || payload.TaskID != taskID || payload.WorkerID != workerID {
		return nil, false, store.ErrLeaseLost
	}
	task, err := s.taskStore.Get(ctx, taskID)
	if err != nil {
		return nil, false, err
	}
	if task.Version != payload.Version+1 {
		return nil, false, nil
	}
	for _, status := range statuses {
		if task.Status == status {
			return task, true, nil
		}
	}
	return nil, false, nil
}

func (s *WorkerTaskService) prepareRunningTask(
	ctx context.Context,
	taskID string,
	workerID string,
	version uint64,
) (*domain.Task, error) {
	if taskID == "" || strings.TrimSpace(workerID) == "" {
		return nil, ErrInvalidWorkerRequest
	}
	task, err := s.taskStore.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if task.Status != domain.TaskStatusRunning ||
		task.LeaseOwner != workerID ||
		task.LeaseExpiresAt == nil ||
		!task.LeaseExpiresAt.After(now) {
		return nil, store.ErrLeaseLost
	}
	if task.Version != version {
		return nil, store.ErrTaskConflict
	}
	return task, nil
}
