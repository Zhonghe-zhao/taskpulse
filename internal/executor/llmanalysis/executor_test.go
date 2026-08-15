package llmanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
)

type failingClient struct {
	err error
}

func (c failingClient) Analyze(context.Context, AnalysisRequest) (AnalysisResponse, error) {
	return AnalysisResponse{}, c.err
}

func TestExecutorCompletesAnalysisWithFakeClient(t *testing.T) {
	executor, err := New(NewFakeClient("fake-model"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task := newLLMAnalysisTask(t, Input{
		Subject: "Go concurrency",
		Notes:   []string{"goroutine", "channel"},
		Goal:    "make a two week plan",
	})

	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Outcome != worker.OutcomeSucceeded {
		t.Fatalf("expected succeeded outcome, got %s", result.Outcome)
	}

	var output Output
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.Subject != "Go concurrency" || output.Model != "fake-model" || len(output.Plan) == 0 {
		t.Fatalf("unexpected output: %+v", output)
	}
}

func TestExecutorRejectsInvalidPromptAsPermanentError(t *testing.T) {
	executor, err := New(NewFakeClient(""))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task := newLLMAnalysisTask(t, Input{
		Subject: " ",
		Goal:    "make a plan",
	})

	_, err = executor.Execute(context.Background(), task)
	executionError, ok := worker.AsExecutionError(err)
	if !ok {
		t.Fatalf("expected ExecutionError, got %v", err)
	}
	if executionError.Kind != worker.ErrorPermanent || executionError.Code != "llm_invalid_prompt" {
		t.Fatalf("unexpected execution error: %+v", executionError)
	}
}

func TestExecutorPreservesTransientProviderError(t *testing.T) {
	transientErr := NewProviderRateLimitError(time.Second, errors.New("429"))
	executor, err := New(failingClient{err: transientErr})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	task := newLLMAnalysisTask(t, Input{
		Subject: "Go",
		Goal:    "summarize notes",
	})

	_, err = executor.Execute(context.Background(), task)
	executionError, ok := worker.AsExecutionError(err)
	if !ok {
		t.Fatalf("expected ExecutionError, got %v", err)
	}
	if executionError.Kind != worker.ErrorTransient ||
		executionError.Code != "llm_rate_limited" ||
		executionError.RetryAfter != time.Second {
		t.Fatalf("unexpected execution error: %+v", executionError)
	}
}

func TestFakeClientCanSimulateRateLimitOnce(t *testing.T) {
	client := NewFakeClient("fake-model")
	if err := client.SetFailureMode(FakeFailureRateLimitedOnce); err != nil {
		t.Fatalf("SetFailureMode returned error: %v", err)
	}
	request := AnalysisRequest{
		Subject: "Go",
		Notes:   []string{"context"},
		Goal:    "summarize notes",
	}

	_, err := client.Analyze(context.Background(), request)
	executionError, ok := worker.AsExecutionError(err)
	if !ok {
		t.Fatalf("expected ExecutionError, got %v", err)
	}
	if executionError.Code != "llm_rate_limited" || executionError.RetryAfter != time.Second {
		t.Fatalf("unexpected first failure: %+v", executionError)
	}

	response, err := client.Analyze(context.Background(), request)
	if err != nil {
		t.Fatalf("second Analyze returned error: %v", err)
	}
	if response.Model != "fake-model" || response.Summary == "" {
		t.Fatalf("unexpected response after retry: %+v", response)
	}
}

func TestFakeClientDelayHonorsContextCancellation(t *testing.T) {
	client := NewFakeClient("fake-llm")
	if err := client.SetDelay(time.Hour); err != nil {
		t.Fatalf("SetDelay returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Analyze(ctx, AnalysisRequest{
		Subject: "Go",
		Goal:    "study",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func newLLMAnalysisTask(t *testing.T, input Input) *domain.Task {
	t.Helper()
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	task, err := domain.NewTask("task_llm", "llm_analysis", payload, 3, time.Now())
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	return task
}
