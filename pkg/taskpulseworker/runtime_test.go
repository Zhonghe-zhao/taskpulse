package taskpulseworker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/pkg/taskpulse"
)

type testExecutor struct {
	called atomic.Int32
}

func TestRuntimeRequiresWorkflowAndMatchingExecutor(t *testing.T) {
	if _, err := New(Config{
		Client:   &taskpulse.Client{BaseURL: "http://127.0.0.1:8085"},
		WorkerID: "worker-1",
	}); err == nil {
		t.Fatal("expected empty workflow to be rejected")
	}

	runtime, err := New(Config{
		Client:   &taskpulse.Client{BaseURL: "http://127.0.0.1:8085"},
		WorkerID: "worker-1",
		Workflow: "workflow.a",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := runtime.Register("workflow.b", &testExecutor{}); err == nil {
		t.Fatal("expected mismatched executor workflow to be rejected")
	}
	if err := runtime.Register("workflow.a", &testExecutor{}); err != nil {
		t.Fatalf("Register matching workflow returned error: %v", err)
	}
	if err := runtime.Register("workflow.a", &testExecutor{}); err == nil {
		t.Fatal("expected duplicate workflow registration to fail")
	}
}

func TestClassifyDefaultsToPermanent(t *testing.T) {
	failure := classify(errors.New("invalid executor configuration"))
	if failure.Retryable {
		t.Fatalf("expected unclassified error to be permanent, got %+v", failure)
	}
}

func (e *testExecutor) Execute(ctx context.Context, task *taskpulse.Task, progress ProgressReporter) (Result, error) {
	e.called.Add(1)
	if err := progress.Report(ctx, 50, "halfway"); err != nil {
		return Result{}, err
	}
	return Result{ResultRef: []byte(`{"reference":"result-1"}`)}, nil
}

func TestRuntimeClaimsProgressesAndCompletes(t *testing.T) {
	var claimCount atomic.Int32
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker/tasks/claim":
			if claimCount.Add(1) > 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSONTest(w, taskpulse.Task{
				ID: "task-1", Workflow: "test.workflow", Status: "running",
				Version: 1, LeaseToken: "token-1",
			})
		case "/worker/tasks/task-1/progress":
			writeJSONTest(w, taskpulse.Task{ID: "task-1", Workflow: "test.workflow", Version: 2, LeaseToken: "token-2"})
		case "/worker/tasks/task-1/complete":
			completed <- struct{}{}
			writeJSONTest(w, taskpulse.Task{ID: "task-1", Workflow: "test.workflow", Status: "succeeded"})
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client:            &taskpulse.Client{BaseURL: server.URL},
		WorkerID:          "worker-1",
		Workflow:          "test.workflow",
		LeaseDuration:     time.Second,
		PollInterval:      time.Millisecond,
		HeartbeatInterval: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	executor := &testExecutor{}
	if err := runtime.Register("test.workflow", executor); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- runtime.Run(ctx) }()
	select {
	case <-completed:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("runtime did not complete task")
	}
	if err := <-runErrors; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if executor.called.Load() != 1 {
		t.Fatalf("expected executor to run once, got %d", executor.called.Load())
	}
}

func TestRuntimeRetriesTransientClaimFailure(t *testing.T) {
	var claimCount atomic.Int32
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker/tasks/claim":
			if claimCount.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeJSONTest(w, taskpulse.Task{
				ID: "task-retry", Workflow: "test.workflow", Status: "running", Version: 1, LeaseToken: "token-1",
			})
		case "/worker/tasks/task-retry/progress":
			writeJSONTest(w, taskpulse.Task{ID: "task-retry", Workflow: "test.workflow", Version: 2, LeaseToken: "token-2"})
		case "/worker/tasks/task-retry/complete":
			completed <- struct{}{}
			writeJSONTest(w, taskpulse.Task{ID: "task-retry", Workflow: "test.workflow", Status: "succeeded"})
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client:            &taskpulse.Client{BaseURL: server.URL},
		WorkerID:          "worker-1",
		Workflow:          "test.workflow",
		LeaseDuration:     time.Second,
		PollInterval:      time.Millisecond,
		HeartbeatInterval: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := runtime.Register("test.workflow", &testExecutor{}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- runtime.Run(ctx) }()
	select {
	case <-completed:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("runtime did not recover from transient claim failure")
	}
	if err := <-runErrors; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if claimCount.Load() < 2 {
		t.Fatalf("expected retry after 503, got %d claim attempts", claimCount.Load())
	}
}

func TestClaimRetryDelayIsCappedExponential(t *testing.T) {
	base := 100 * time.Millisecond
	maximum := time.Second
	for failures, want := range map[int]time.Duration{
		1: 100 * time.Millisecond,
		2: 200 * time.Millisecond,
		3: 400 * time.Millisecond,
		4: 800 * time.Millisecond,
		5: time.Second,
		8: time.Second,
	} {
		if got := claimRetryDelay(base, maximum, failures); got != want {
			t.Fatalf("claimRetryDelay(%s, %s, %d) = %s, want %s", base, maximum, failures, got, want)
		}
	}
}

func TestIsRetryableClaimErrorClassifiesTimeoutSeparatelyFromCancellation(t *testing.T) {
	if !isRetryableClaimError(context.DeadlineExceeded) {
		t.Fatal("expected claim timeout to be retryable")
	}
	if isRetryableClaimError(context.Canceled) {
		t.Fatal("expected canceled worker context to stop the runtime")
	}
}

func TestRuntimeReturnsPermanentClaimFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client: &taskpulse.Client{BaseURL: server.URL}, WorkerID: "worker-1", Workflow: "test.workflow",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	err = runtime.Run(context.Background())
	if err == nil {
		t.Fatal("expected permanent claim failure")
	}
}

func TestRuntimeRejectsMalformedClaimResponseBeforeExecuting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/worker/tasks/claim" {
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSONTest(w, taskpulse.Task{
			ID: "task-wrong-workflow", Workflow: "other.workflow", Status: "running", LeaseToken: "token-1",
		})
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client: &taskpulse.Client{BaseURL: server.URL}, WorkerID: "worker-1", Workflow: "test.workflow",
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	executor := &testExecutor{}
	if err := runtime.Register("test.workflow", executor); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if err := runtime.Run(context.Background()); err == nil {
		t.Fatal("expected malformed claim response to stop the runtime")
	}
	if executor.called.Load() != 0 {
		t.Fatalf("executor should not run for malformed claim response, got %d calls", executor.called.Load())
	}
}

func TestRuntimeShutdownReleasesClaimedTask(t *testing.T) {
	var failCount atomic.Int32
	type releasePayload struct {
		WorkerID   string `json:"worker_id"`
		LeaseToken string `json:"lease_token"`
		Version    uint64 `json:"version"`
	}
	released := make(chan releasePayload, 1)
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker/tasks/claim":
			writeJSONTest(w, taskpulse.Task{ID: "task-1", Workflow: "test.workflow", Status: "running", Version: 1, LeaseToken: "token-1"})
		case "/worker/tasks/task-1/release":
			var request releasePayload
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode release request: %v", err)
			}
			released <- request
			writeJSONTest(w, taskpulse.Task{ID: "task-1", Workflow: "test.workflow", Status: "queued", Version: 2})
		case "/worker/tasks/task-1/fail":
			failCount.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client:          &taskpulse.Client{BaseURL: server.URL},
		WorkerID:        "worker-1",
		Workflow:        "test.workflow",
		PollInterval:    time.Millisecond,
		LeaseDuration:   time.Second,
		ShutdownTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := runtime.Register("test.workflow", ExecutorFunc(func(ctx context.Context, task *taskpulse.Task, progress ProgressReporter) (Result, error) {
		started <- struct{}{}
		<-ctx.Done()
		return Result{}, ctx.Err()
	})); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- runtime.Run(ctx) }()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("runtime did not start executor")
	}
	if err := <-runErrors; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if failCount.Load() != 0 {
		t.Fatalf("shutdown incorrectly reported task failure: %d", failCount.Load())
	}
	select {
	case request := <-released:
		if request.WorkerID != "worker-1" || request.LeaseToken != "token-1" || request.Version != 1 {
			t.Fatalf("unexpected release request: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not release claimed task")
	}
}

func TestRuntimeFailsRetryablyWhenExecutorExceedsExecutionTimeout(t *testing.T) {
	type failPayload struct {
		WorkerID   string `json:"worker_id"`
		LeaseToken string `json:"lease_token"`
		Version    uint64 `json:"version"`
		ErrorCode  string `json:"error_code"`
		Retryable  bool   `json:"retryable"`
	}
	failed := make(chan failPayload, 1)
	var claimCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker/tasks/claim":
			if claimCount.Add(1) > 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSONTest(w, taskpulse.Task{ID: "task-timeout", Workflow: "test.workflow", Status: "running", Version: 1, LeaseToken: "token-1"})
		case "/worker/tasks/task-timeout/fail":
			var request failPayload
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode fail request: %v", err)
			}
			failed <- request
			writeJSONTest(w, taskpulse.Task{ID: "task-timeout", Workflow: "test.workflow", Status: "retrying"})
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client: &taskpulse.Client{BaseURL: server.URL}, WorkerID: "worker-1", Workflow: "test.workflow",
		LeaseDuration: time.Second, PollInterval: time.Millisecond, HeartbeatInterval: 500 * time.Millisecond,
		ExecutionTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := runtime.Register("test.workflow", ExecutorFunc(func(ctx context.Context, task *taskpulse.Task, progress ProgressReporter) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErrors := make(chan error, 1)
	go func() { runErrors <- runtime.Run(ctx) }()
	select {
	case request := <-failed:
		if request.WorkerID != "worker-1" || request.LeaseToken != "token-1" || request.Version != 1 ||
			request.ErrorCode != "execution_timeout" || !request.Retryable {
			t.Fatalf("unexpected timeout failure: %+v", request)
		}
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("runtime did not report execution timeout")
	}
	if err := <-runErrors; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRuntimeReportsExecutionTimeoutWhenExecutorIgnoresContext(t *testing.T) {
	type failPayload struct {
		ErrorCode string `json:"error_code"`
		Retryable bool   `json:"retryable"`
	}
	failed := make(chan failPayload, 1)
	executorMayReturn := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/worker/tasks/claim":
			writeJSONTest(w, taskpulse.Task{ID: "task-timeout-ignored", Workflow: "test.workflow", Status: "running", Version: 1, LeaseToken: "token-1"})
		case "/worker/tasks/task-timeout-ignored/fail":
			var request failPayload
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode fail request: %v", err)
			}
			failed <- request
			writeJSONTest(w, taskpulse.Task{ID: "task-timeout-ignored", Workflow: "test.workflow", Status: "retrying"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	runtime, err := New(Config{
		Client: &taskpulse.Client{BaseURL: server.URL}, WorkerID: "worker-1", Workflow: "test.workflow",
		LeaseDuration: time.Second, PollInterval: time.Second, HeartbeatInterval: 500 * time.Millisecond,
		ExecutionTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := runtime.Register("test.workflow", ExecutorFunc(func(context.Context, *taskpulse.Task, ProgressReporter) (Result, error) {
		<-executorMayReturn
		return Result{}, nil
	})); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- runtime.Run(ctx) }()
	select {
	case request := <-failed:
		if request.ErrorCode != "execution_timeout" || !request.Retryable {
			t.Fatalf("unexpected timeout failure: %+v", request)
		}
	case <-time.After(time.Second):
		cancel()
		close(executorMayReturn)
		t.Fatal("runtime did not report timeout for context-ignoring executor")
	}
	cancel()
	close(executorMayReturn)
	if err := <-runErrors; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

type ExecutorFunc func(context.Context, *taskpulse.Task, ProgressReporter) (Result, error)

func (f ExecutorFunc) Execute(ctx context.Context, task *taskpulse.Task, progress ProgressReporter) (Result, error) {
	return f(ctx, task, progress)
}

func writeJSONTest(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
