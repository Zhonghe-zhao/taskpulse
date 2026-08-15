package llmanalysis

import (
	"context"
	"errors"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
)

type Client interface {
	Analyze(ctx context.Context, request AnalysisRequest) (AnalysisResponse, error)
}

var (
	ErrNilClient         = errors.New("llm analysis client is nil")
	ErrEmptySubject      = errors.New("subject is required")
	ErrEmptyGoal         = errors.New("goal is required")
	ErrEmptyLLMOutput    = errors.New("llm output is empty")
	ErrProviderTimeout   = errors.New("llm provider timeout")
	ErrProviderRateLimit = errors.New("llm provider rate limited")
	ErrProviderInternal  = errors.New("llm provider internal error")
	ErrInvalidPrompt     = errors.New("invalid llm prompt")
)

func NewProviderTimeoutError(cause error) error {
	return newExecutionError(worker.ErrorTransient, "llm_timeout", 0, cause)
}

func NewProviderRateLimitError(retryAfter time.Duration, cause error) error {
	return newExecutionError(worker.ErrorTransient, "llm_rate_limited", retryAfter, cause)
}

func NewProviderInternalError(cause error) error {
	return newExecutionError(worker.ErrorTransient, "llm_provider_5xx", 0, cause)
}

func NewInvalidPromptError(cause error) error {
	return newExecutionError(worker.ErrorPermanent, "llm_invalid_prompt", 0, cause)
}

func newExecutionError(kind worker.ErrorKind, code string, retryAfter time.Duration, cause error) error {
	executionError, err := worker.NewExecutionError(kind, code, retryAfter, cause)
	if err != nil {
		return err
	}
	return executionError
}
