package taskpulseworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/pkg/taskpulse"
)

type Result struct {
	Output    []byte
	ResultRef []byte
}

type ProgressReporter interface {
	Report(ctx context.Context, progress int, message string) error
}

type Executor interface {
	Execute(ctx context.Context, task *taskpulse.Task, progress ProgressReporter) (Result, error)
}

type Failure struct {
	Code       string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Err        error
}

func (f *Failure) Error() string {
	if f == nil {
		return "task failure"
	}
	if f.Err != nil {
		return f.Message + ": " + f.Err.Error()
	}
	return f.Message
}

func (f *Failure) Unwrap() error { return f.Err }

func Retryable(code, message string, err error) error {
	return &Failure{Code: code, Message: message, Retryable: true, Err: err}
}

func Permanent(code, message string, err error) error {
	return &Failure{Code: code, Message: message, Retryable: false, Err: err}
}

type Config struct {
	Client   *taskpulse.Client
	WorkerID string
	// Workflow identifies the single workflow this runtime is allowed to claim.
	// A runtime must never claim unknown workflows and fail them by accident.
	Workflow      string
	LeaseDuration time.Duration
	PollInterval  time.Duration
	// ClaimRetryMaxInterval caps exponential backoff when the TaskPulse
	// control plane is temporarily unavailable. It does not affect task-level
	// retry policy after a task has been claimed.
	ClaimRetryMaxInterval time.Duration
	HeartbeatInterval     time.Duration
	// ExecutionTimeout bounds one executor attempt. Zero disables the local
	// timeout; it is distinct from LeaseDuration, which proves ownership.
	ExecutionTimeout time.Duration
	// ShutdownTimeout bounds how long Run waits for an executor to observe
	// cancellation before releasing its lease during graceful shutdown.
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

type Runtime struct {
	client                *taskpulse.Client
	workerID              string
	workflow              string
	leaseDuration         time.Duration
	pollInterval          time.Duration
	claimRetryMaxInterval time.Duration
	heartbeatInterval     time.Duration
	executionTimeout      time.Duration
	shutdownTimeout       time.Duration
	logger                *slog.Logger
	executors             map[string]Executor
}

type executionOutcome struct {
	result Result
	err    error
}

func New(config Config) (*Runtime, error) {
	if config.Client == nil {
		return nil, errors.New("taskpulse client is required")
	}
	if config.WorkerID == "" {
		return nil, errors.New("worker id is required")
	}
	if strings.TrimSpace(config.Workflow) == "" {
		return nil, errors.New("workflow is required")
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.ClaimRetryMaxInterval <= 0 {
		config.ClaimRetryMaxInterval = 5 * time.Second
	}
	if config.ClaimRetryMaxInterval < config.PollInterval {
		config.ClaimRetryMaxInterval = config.PollInterval
	}
	if config.HeartbeatInterval <= 0 || config.HeartbeatInterval >= config.LeaseDuration {
		config.HeartbeatInterval = config.LeaseDuration / 3
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 5 * time.Second
	}
	if config.ExecutionTimeout < 0 {
		return nil, errors.New("execution timeout cannot be negative")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &Runtime{
		client: config.Client, workerID: config.WorkerID, workflow: strings.TrimSpace(config.Workflow),
		leaseDuration: config.LeaseDuration, pollInterval: config.PollInterval,
		claimRetryMaxInterval: config.ClaimRetryMaxInterval,
		heartbeatInterval:     config.HeartbeatInterval,
		executionTimeout:      config.ExecutionTimeout,
		shutdownTimeout:       config.ShutdownTimeout,
		logger:                config.Logger,
		executors:             make(map[string]Executor),
	}, nil
}

func (r *Runtime) Register(workflow string, executor Executor) error {
	if workflow == "" {
		return errors.New("workflow is required")
	}
	if executor == nil {
		return errors.New("executor is required")
	}
	if workflow != r.workflow {
		return fmt.Errorf("executor workflow %q does not match runtime workflow %q", workflow, r.workflow)
	}
	if _, exists := r.executors[workflow]; exists {
		return fmt.Errorf("executor already registered for workflow %q", workflow)
	}
	r.executors[workflow] = executor
	return nil
}

func (r *Runtime) Run(ctx context.Context) error {
	claimFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		task, err := r.client.Claim(ctx, taskpulse.ClaimRequest{
			WorkerID: r.workerID, Workflow: r.workflow, LeaseDuration: r.leaseDuration,
		})
		if errors.Is(err, taskpulse.ErrNoTaskAvailable) {
			claimFailures = 0
			if err := wait(ctx, r.pollInterval); err != nil {
				return nil
			}
			continue
		}
		if err != nil {
			if isRetryableClaimError(err) {
				claimFailures++
				delay := claimRetryDelay(r.pollInterval, r.claimRetryMaxInterval, claimFailures)
				r.logger.Warn("claim temporarily unavailable; retrying", "worker_id", r.workerID,
					"attempt", claimFailures, "retry_delay", delay, "error", err)
				if err := wait(ctx, delay); err != nil {
					return nil
				}
				continue
			}
			return fmt.Errorf("claim task: %w", err)
		}
		if err := r.validateClaimedTask(task); err != nil {
			return err
		}
		claimFailures = 0
		r.logger.Info("task claimed", "task_id", task.ID, "workflow", task.Workflow,
			"worker_id", r.workerID, "version", task.Version, "lease_until", task.LeaseUntil)
		if err := r.execute(ctx, task); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			r.logger.Error("task execution loop failed", "task_id", task.ID, "error", err)
		}
	}
}

func (r *Runtime) validateClaimedTask(task *taskpulse.Task) error {
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return errors.New("invalid claim response: task id is required")
	}
	if task.Workflow != r.workflow {
		return fmt.Errorf("invalid claim response for task %q: workflow %q does not match Worker workflow %q", task.ID, task.Workflow, r.workflow)
	}
	if task.Status != "running" {
		return fmt.Errorf("invalid claim response for task %q: status %q is not running", task.ID, task.Status)
	}
	if strings.TrimSpace(task.LeaseToken) == "" {
		return fmt.Errorf("invalid claim response for task %q: lease token is required", task.ID)
	}
	return nil
}

func claimRetryDelay(base, maximum time.Duration, failures int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if maximum < base {
		maximum = base
	}
	if failures <= 1 {
		return base
	}
	delay := base
	for attempt := 1; attempt < failures && delay < maximum; attempt++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return delay
}

func isRetryableClaimError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	// http.Client timeouts commonly wrap context.DeadlineExceeded. A timeout
	// while claiming is a temporary control-plane failure, not a malformed
	// Worker request. Run exits normally when its parent context is done.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var httpErr *taskpulse.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusRequestTimeout ||
			httpErr.StatusCode == http.StatusTooManyRequests ||
			httpErr.StatusCode >= http.StatusInternalServerError
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func (r *Runtime) execute(parent context.Context, task *taskpulse.Task) error {
	executor, exists := r.executors[task.Workflow]
	if !exists {
		return r.fail(parent, task, nil, Permanent("executor_not_found", "no executor registered", nil))
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	executionCtx := ctx
	stopExecutionTimeout := func() {}
	if r.executionTimeout > 0 {
		executionCtx, stopExecutionTimeout = context.WithTimeout(ctx, r.executionTimeout)
	}
	defer stopExecutionTimeout()
	state := &leaseState{token: task.LeaseToken, version: task.Version}
	heartbeatErrors := make(chan error, 1)
	go r.heartbeat(ctx, cancel, task.ID, state, heartbeatErrors)

	progress := &progressReporter{client: r.client, taskID: task.ID, workerID: r.workerID, state: state}
	executionDone := make(chan executionOutcome, 1)
	go func() {
		result, err := executor.Execute(executionCtx, task, progress)
		executionDone <- executionOutcome{result: result, err: err}
	}()

	var outcome executionOutcome
	var heartbeatErr error
	select {
	case outcome = <-executionDone:
		cancel()
		// Completion and a shutdown signal can race. Once shutdown has been
		// observed, prefer handing the lease back over submitting a terminal
		// transition from a process that is about to exit.
		if parent.Err() != nil {
			if err := r.release(task, state); err != nil {
				r.logger.Error("graceful task release failed; lease expiry recovery remains available",
					"task_id", task.ID, "workflow", task.Workflow, "worker_id", r.workerID, "error", err)
			}
			return nil
		}
	case <-parent.Done():
		// The executor receives the same cancellation through ctx. Give it a
		// bounded opportunity to stop its own work, then release the lease so a
		// healthy worker can continue immediately instead of waiting for expiry.
		cancel()
		timer := time.NewTimer(r.shutdownTimeout)
		select {
		case <-executionDone:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			r.logger.Warn("executor did not stop before shutdown grace period", "task_id", task.ID,
				"workflow", task.Workflow, "worker_id", r.workerID, "shutdown_timeout", r.shutdownTimeout)
		}
		if err := r.release(task, state); err != nil {
			r.logger.Error("graceful task release failed; lease expiry recovery remains available",
				"task_id", task.ID, "workflow", task.Workflow, "worker_id", r.workerID, "error", err)
		}
		return nil
	case <-executionCtx.Done():
		if parent.Err() != nil {
			cancel()
			if err := r.release(task, state); err != nil {
				r.logger.Error("graceful task release failed; lease expiry recovery remains available",
					"task_id", task.ID, "workflow", task.Workflow, "worker_id", r.workerID, "error", err)
			}
			return nil
		}
		select {
		case heartbeatErr = <-heartbeatErrors:
			return r.fail(parent, task, state, Retryable("heartbeat_failed", "task heartbeat failed", heartbeatErr))
		default:
		}
		if errors.Is(executionCtx.Err(), context.DeadlineExceeded) {
			// Stop extending the lease before reporting the timed-out attempt.
			// The Executor may ignore Context and continue running in its goroutine;
			// the system is therefore still at-least-once and business effects must
			// remain idempotent.
			cancel()
			return r.fail(parent, task, state, Retryable(
				"execution_timeout",
				fmt.Sprintf("executor exceeded %s execution timeout", r.executionTimeout),
				context.DeadlineExceeded,
			))
		}
		return nil
	}

	select {
	case heartbeatErr = <-heartbeatErrors:
	default:
	}
	if heartbeatErr != nil {
		return r.fail(parent, task, state, Retryable("heartbeat_failed", "task heartbeat failed", heartbeatErr))
	}
	if outcome.err != nil {
		return r.fail(parent, task, state, classify(outcome.err))
	}

	state.mu.Lock()
	request := taskpulse.CompleteRequest{
		TaskID: task.ID, WorkerID: r.workerID, LeaseToken: state.token,
		Version: state.version, Output: outcome.result.Output, ResultRef: outcome.result.ResultRef,
	}
	state.mu.Unlock()
	if _, err := r.client.Complete(parent, request); err != nil {
		return fmt.Errorf("complete task %q: %w", task.ID, err)
	}
	r.logger.Info("task completed", "task_id", task.ID, "workflow", task.Workflow, "worker_id", r.workerID)
	return nil
}

func (r *Runtime) release(task *taskpulse.Task, state *leaseState) error {
	state.mu.Lock()
	token, version := state.token, state.version
	state.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), r.shutdownTimeout)
	defer cancel()
	if _, err := r.client.Release(ctx, taskpulse.ReleaseRequest{
		TaskID: task.ID, WorkerID: r.workerID, LeaseToken: token, Version: version,
	}); err != nil {
		return fmt.Errorf("release task %q: %w", task.ID, err)
	}
	r.logger.Info("task released for graceful shutdown", "task_id", task.ID, "workflow", task.Workflow,
		"worker_id", r.workerID)
	return nil
}

func (r *Runtime) heartbeat(ctx context.Context, cancel context.CancelFunc, taskID string, state *leaseState, errorsOut chan<- error) {
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state.mu.Lock()
			token, version := state.token, state.version
			state.mu.Unlock()
			task, err := r.client.Heartbeat(ctx, taskID, r.workerID, token, r.leaseDuration)
			if err != nil {
				if ctx.Err() == nil {
					errorsOut <- err
					cancel()
				}
				return
			}
			state.mu.Lock()
			state.token = task.LeaseToken
			if task.Version != 0 || version == 0 {
				state.version = task.Version
			}
			state.mu.Unlock()
		}
	}
}

func (r *Runtime) fail(ctx context.Context, task *taskpulse.Task, state *leaseState, failure error) error {
	if ctx.Err() != nil {
		return nil
	}
	classified := classify(failure)
	if state == nil {
		state = &leaseState{token: task.LeaseToken, version: task.Version}
	}
	state.mu.Lock()
	token, version := state.token, state.version
	state.mu.Unlock()
	if _, err := r.client.Fail(ctx, taskpulse.FailRequest{
		TaskID: task.ID, WorkerID: r.workerID, LeaseToken: token, Version: version,
		ErrorCode: classified.Code, ErrorMessage: classified.Message,
		Retryable: classified.Retryable, RetryAfter: classified.RetryAfter,
	}); err != nil {
		return fmt.Errorf("fail task %q: %w", task.ID, err)
	}
	r.logger.Warn("task failed", "task_id", task.ID, "workflow", task.Workflow, "error_code", classified.Code, "retryable", classified.Retryable)
	return nil
}

type leaseState struct {
	mu      sync.Mutex
	token   string
	version uint64
}

type progressReporter struct {
	client   *taskpulse.Client
	taskID   string
	workerID string
	state    *leaseState
}

func (p *progressReporter) Report(ctx context.Context, progress int, message string) error {
	p.state.mu.Lock()
	token, version := p.state.token, p.state.version
	p.state.mu.Unlock()
	task, err := p.client.Progress(ctx, p.taskID, p.workerID, token, version, progress, message)
	if err != nil {
		return err
	}
	p.state.mu.Lock()
	p.state.token = task.LeaseToken
	p.state.version = task.Version
	p.state.mu.Unlock()
	return nil
}

func classify(err error) *Failure {
	if err == nil {
		return &Failure{Code: "worker_error", Message: "task failed", Retryable: true}
	}
	var failure *Failure
	if errors.As(err, &failure) {
		if failure.Code == "" {
			failure.Code = "worker_error"
		}
		if failure.Message == "" {
			failure.Message = err.Error()
		}
		return failure
	}
	// An Executor must explicitly classify an upstream failure as retryable.
	// Retrying arbitrary implementation or input errors amplifies failures and
	// can repeatedly charge an LLM or external API without any recovery path.
	return &Failure{Code: "worker_error", Message: err.Error(), Retryable: false, Err: err}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
