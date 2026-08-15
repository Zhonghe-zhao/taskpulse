package worker

import (
	"context"
	"errors"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/identity"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

var (
	ErrNilRetryTransitionStore = errors.New("retry transition store is nil")
	ErrNilBackoffCalculator    = errors.New("backoff calculator is nil")
	ErrExecutionNotRetryable   = errors.New("execution error is not retryable")
	ErrInvalidRetryNow         = errors.New("retry scheduling time is zero")
)

type RetryScheduler struct {
	transitionStore store.TaskTransitionStore
	backoff         *BackoffCalculator
	newEventID      func() string
}

func NewRetryScheduler(
	transitionStore store.TaskTransitionStore,
	backoff *BackoffCalculator,
) (*RetryScheduler, error) {
	if transitionStore == nil {
		return nil, ErrNilRetryTransitionStore
	}
	if backoff == nil {
		return nil, ErrNilBackoffCalculator
	}
	return &RetryScheduler{
		transitionStore: transitionStore,
		backoff:         backoff,
		newEventID:      func() string { return identity.New("event") },
	}, nil
}

func (s *RetryScheduler) Schedule(
	ctx context.Context,
	task *domain.Task,
	executionError *ExecutionError,
	policy RetryPolicy,
	now time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if task == nil {
		return store.ErrNilTask
	}
	if executionError == nil {
		return ErrNilExecutionError
	}
	if err := executionError.Validate(); err != nil {
		return err
	}
	if !executionError.Retryable() {
		return ErrExecutionNotRetryable
	}
	if now.IsZero() {
		return ErrInvalidRetryNow
	}
	if err := policy.Validate(); err != nil {
		return err
	}

	nextRetryCount := task.RetryCount + 1
	if nextRetryCount > task.MaxRetries || nextRetryCount > policy.MaxRetries {
		return domain.ErrRetryBudgetExhausted
	}
	delay, err := s.backoff.Delay(policy, nextRetryCount, executionError.RetryAfter)
	if err != nil {
		return err
	}
	if err := task.ScheduleRetry(now, now.Add(delay)); err != nil {
		return err
	}
	task.ErrorMessage = executionError.Code

	event, err := domain.NewTaskRetryingEvent(
		s.newEventID(),
		task,
		executionError.Code,
		delay,
		now,
	)
	if err != nil {
		return err
	}
	return s.transitionStore.UpdateTaskWithEvent(ctx, task, event)
}
