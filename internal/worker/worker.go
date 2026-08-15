package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/identity"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

type ExecutionOutcome string

const (
	OutcomeSucceeded ExecutionOutcome = "succeeded"
	OutcomePartial   ExecutionOutcome = "partially_succeeded"
	OutcomeFailed    ExecutionOutcome = "failed"
)

type ExecutionResult struct {
	Output       json.RawMessage
	Outcome      ExecutionOutcome
	ErrorMessage string
}

type Executor interface {
	Execute(ctx context.Context, task *domain.Task) (ExecutionResult, error)
}

type MetricsRecorder interface {
	RecordClaimAttempt(workflow string)
	RecordClaimMiss(workflow string)
	RecordTaskClaimed(workflow string)
	RecordTaskCompleted(workflow string, status domain.TaskStatus, duration time.Duration)
	RecordTaskRetried(workflow string, errorCode string)
	RecordLeaseRenewed(workflow string)
	RecordLeaseLost(workflow string)
}

type noopMetricsRecorder struct{}

func (noopMetricsRecorder) RecordClaimAttempt(string) {}
func (noopMetricsRecorder) RecordClaimMiss(string)    {}
func (noopMetricsRecorder) RecordTaskClaimed(string)  {}
func (noopMetricsRecorder) RecordTaskCompleted(string, domain.TaskStatus, time.Duration) {
}
func (noopMetricsRecorder) RecordTaskRetried(string, string) {}
func (noopMetricsRecorder) RecordLeaseRenewed(string)        {}
func (noopMetricsRecorder) RecordLeaseLost(string)           {}

type Worker struct {
	taskStore       store.TaskStore
	transitionStore store.TaskTransitionStore
	executors       map[string]Executor
	retryPolicies   map[string]RetryPolicy
	retryScheduler  *RetryScheduler
	logger          *slog.Logger
	metrics         MetricsRecorder
	id              string
	leaseDuration   time.Duration
	now             func() time.Time
}

const defaultLeaseDuration = 30 * time.Second

var ErrInvalidLeaseDuration = errors.New("lease duration must be positive")

func New(
	taskStore store.TaskStore,
	transitionStore store.TaskTransitionStore,
	executors map[string]Executor,
	retryPolicies map[string]RetryPolicy,
) *Worker {
	copiedExecutors := make(map[string]Executor, len(executors))
	for workflow, executor := range executors {
		copiedExecutors[workflow] = executor
	}
	copiedRetryPolicies := make(map[string]RetryPolicy, len(retryPolicies))
	for workflow, policy := range retryPolicies {
		copiedRetryPolicies[workflow] = policy
	}

	return &Worker{
		taskStore:       taskStore,
		transitionStore: transitionStore,
		executors:       copiedExecutors,
		retryPolicies:   copiedRetryPolicies,
		retryScheduler: &RetryScheduler{
			transitionStore: transitionStore,
			backoff:         NewDefaultBackoffCalculator(),
			newEventID:      func() string { return identity.New("event") },
		},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics:       noopMetricsRecorder{},
		id:            identity.New("worker"),
		leaseDuration: defaultLeaseDuration,
		now:           time.Now,
	}
}

func (w *Worker) WithLogger(logger *slog.Logger) *Worker {
	if logger != nil {
		w.logger = logger
	}
	return w
}

func (w *Worker) WithMetrics(metrics MetricsRecorder) *Worker {
	if metrics != nil {
		w.metrics = metrics
	}
	return w
}

// SetLeaseDuration configures how long a claimed task remains owned by a Worker.
func (w *Worker) SetLeaseDuration(leaseDuration time.Duration) error {
	if leaseDuration <= 0 {
		return ErrInvalidLeaseDuration
	}
	w.leaseDuration = leaseDuration
	return nil
}

// 当前逻辑Worker从队列中领取任务 添加事件 识别工作流 执行相应任务
func (w *Worker) ProcessNext(ctx context.Context) (bool, error) {
	now := w.now()
	w.metrics.RecordClaimAttempt("all")
	task, err := w.transitionStore.ClaimNextWithEvent(
		ctx,
		store.ClaimOptions{
			WorkerID:      w.id,
			Now:           now,
			LeaseDuration: w.leaseDuration,
		},
		identity.New("event"),
	)
	if errors.Is(err, store.ErrNoTaskAvailable) {
		w.metrics.RecordClaimMiss("all")
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim task and append event: %w", err)
	}
	w.metrics.RecordTaskClaimed(task.Workflow)
	w.logger.Info(
		"task claimed",
		"task_id", task.ID,
		"workflow", task.Workflow,
		"worker_id", w.id,
		"retry_count", task.RetryCount,
		"lease_expires_at", task.LeaseExpiresAt,
	)

	executor, exists := w.executors[task.Workflow]
	if !exists {
		err := fmt.Errorf("no executor registered for workflow %q", task.Workflow)
		return true, w.finishFailed(ctx, task, err)
	}

	executionStartedAt := w.now()
	result, executeErr := w.executeWithHeartbeat(ctx, task, executor)
	if executeErr != nil {
		if errors.Is(executeErr, store.ErrLeaseLost) {
			return true, executeErr
		}
		if executionError, ok := AsExecutionError(executeErr); ok {
			if executionError.Retryable() {
				policy, configured := w.retryPolicies[task.Workflow]
				if configured {
					scheduleErr := w.retryScheduler.Schedule(
						ctx,
						task,
						executionError,
						policy,
						w.now(),
					)
					if scheduleErr == nil {
						w.metrics.RecordTaskRetried(task.Workflow, executionError.Code)
						w.logger.Warn(
							"task scheduled for retry",
							"task_id", task.ID,
							"workflow", task.Workflow,
							"worker_id", w.id,
							"error_code", executionError.Code,
							"retry_after", executionError.RetryAfter,
							"retry_count", task.RetryCount,
							"available_at", task.AvailableAt,
						)
						return true, nil
					}
					if !errors.Is(scheduleErr, domain.ErrRetryBudgetExhausted) &&
						!errors.Is(scheduleErr, ErrInvalidRetryCount) {
						return true, fmt.Errorf("schedule task retry: %w", scheduleErr)
					}
				}
			}
			executeErr = errors.New(executionError.Code)
		}
		return true, w.finishFailed(ctx, task, executeErr, w.now().Sub(executionStartedAt))
	}
	return true, w.finishWithResult(ctx, task, result, w.now().Sub(executionStartedAt))
}

func (w *Worker) executeWithHeartbeat( // 主流程：执行 Executor 后台流程：定期为任务续租
	ctx context.Context,
	task *domain.Task,
	executor Executor,
) (ExecutionResult, error) {
	executionCtx, cancel := context.WithCancel(ctx) //这里从 Worker 的上层 Context 派生出一个子 Context：心跳续租失败时，只取消当前任务的 Executor。
	defer cancel()

	heartbeatResult := make(chan error, 1)
	go w.maintainLease(executionCtx, cancel, task, heartbeatResult) //启动心跳

	result, executeErr := executor.Execute(executionCtx, task)
	cancel()
	heartbeatErr := <-heartbeatResult
	if heartbeatErr != nil {
		return ExecutionResult{}, heartbeatErr
	}
	return result, executeErr
}

func (w *Worker) maintainLease( // 每隔一段时间调用 TaskStore.RenewLease
	ctx context.Context,
	cancelExecution context.CancelFunc,
	task *domain.Task,
	result chan<- error,
) {
	interval := w.leaseDuration / 3
	if interval <= 0 {
		interval = w.leaseDuration
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case now := <-ticker.C:
			err := w.taskStore.RenewLease(ctx, store.RenewLeaseOptions{
				TaskID:        task.ID,
				WorkerID:      w.id,
				Now:           now,
				LeaseDuration: w.leaseDuration,
			})
			if err != nil {
				// The main execution path cancels this context after the Executor
				// returns. A renewal already in flight may then report context.Canceled;
				// that is normal shutdown, not a lost lease.
				if ctx.Err() != nil {
					result <- nil
					return
				}
				cancelExecution()
				w.metrics.RecordLeaseLost(task.Workflow)
				w.logger.Warn(
					"task lease renewal failed",
					"task_id", task.ID,
					"workflow", task.Workflow,
					"worker_id", w.id,
					"error", err,
				)
				result <- fmt.Errorf("renew task %q lease: %w", task.ID, err)
				return
			}
			w.metrics.RecordLeaseRenewed(task.Workflow)
			w.logger.Debug(
				"task lease renewed",
				"task_id", task.ID,
				"workflow", task.Workflow,
				"worker_id", w.id,
				"lease_expires_at", now.Add(w.leaseDuration),
			)
		}
	}
}

func (w *Worker) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		return errors.New("poll interval must be positive")
	}

	for {
		processed, err := w.ProcessNext(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, store.ErrLeaseLost) {
				continue
			}
			return err
		}
		if processed {
			continue
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) finishWithResult(ctx context.Context, task *domain.Task, result ExecutionResult, duration time.Duration) error { // 根据 ExecutionResult 更新任务状态
	task.Result = result.Output
	task.Progress = 100
	task.ErrorMessage = result.ErrorMessage

	var status domain.TaskStatus
	var eventType domain.EventType
	var message string
	switch result.Outcome {
	case OutcomeSucceeded:
		status, eventType, message = domain.TaskStatusSucceeded, domain.EventTaskSucceeded, "task succeeded"
	case OutcomePartial:
		status, eventType, message = domain.TaskStatusPartial, domain.EventTaskPartial, "task partially succeeded"
	case OutcomeFailed:
		status, eventType, message = domain.TaskStatusFailed, domain.EventTaskFailed, "task failed"
		if task.ErrorMessage == "" {
			task.ErrorMessage = "executor reported failure"
		}
	default:
		return w.finishFailed(ctx, task, fmt.Errorf("invalid execution outcome %q", result.Outcome))
	}

	completedAt := w.now()
	if err := task.MoveTo(status, completedAt); err != nil {
		return fmt.Errorf("move task to %s: %w", status, err)
	}
	event, err := w.newEvent(task, eventType, message, completedAt)
	if err != nil {
		return fmt.Errorf("create %s event: %w", status, err)
	}
	if err := w.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return fmt.Errorf("save %s task and event: %w", status, err)
	}
	w.metrics.RecordTaskCompleted(task.Workflow, status, duration)
	logArgs := []any{
		"task_id", task.ID,
		"workflow", task.Workflow,
		"worker_id", w.id,
		"status", status,
		"retry_count", task.RetryCount,
		"duration_ms", duration.Milliseconds(),
	}
	switch status {
	case domain.TaskStatusSucceeded:
		w.logger.Info("task succeeded", logArgs...)
	case domain.TaskStatusPartial:
		w.logger.Warn("task partially succeeded", logArgs...)
	case domain.TaskStatusFailed:
		w.logger.Error("task failed", logArgs...)
	}
	return nil
}

func (w *Worker) finishFailed(ctx context.Context, task *domain.Task, cause error, duration ...time.Duration) error {
	task.ErrorMessage = cause.Error()
	failedAt := w.now()
	if err := task.MoveTo(domain.TaskStatusFailed, failedAt); err != nil {
		return fmt.Errorf("mark task failed: %w", err)
	}
	event, err := w.newEvent(task, domain.EventTaskFailed, "task failed", failedAt)
	if err != nil {
		return fmt.Errorf("create task failed event: %w", err)
	}
	if err := w.transitionStore.UpdateTaskWithEvent(ctx, task, event); err != nil {
		return fmt.Errorf("save failed task and event: %w", err)
	}
	var elapsed time.Duration
	if len(duration) > 0 {
		elapsed = duration[0]
	}
	w.metrics.RecordTaskCompleted(task.Workflow, domain.TaskStatusFailed, elapsed)
	w.logger.Error(
		"task failed",
		"task_id", task.ID,
		"workflow", task.Workflow,
		"worker_id", w.id,
		"retry_count", task.RetryCount,
		"duration_ms", elapsed.Milliseconds(),
		"error", cause,
	)
	return nil
}

func (w *Worker) newEvent(
	task *domain.Task,
	eventType domain.EventType,
	message string,
	now time.Time,
) (*domain.TaskEvent, error) {
	event, err := domain.NewTaskEvent(
		identity.New("event"), task.ID, eventType, message,
		json.RawMessage("{}"), task.Progress, now,
	)
	if err != nil {
		return nil, err
	}
	return event, nil
}
