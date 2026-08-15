package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/identity"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

type Reaper struct {
	transitionStore store.TaskTransitionStore
	logger          *slog.Logger
	metrics         ReaperMetricsRecorder
	now             func() time.Time
}

type ReaperMetricsRecorder interface {
	RecordReaperExpiredFailure(workflow string)
}

type noopReaperMetricsRecorder struct{}

func (noopReaperMetricsRecorder) RecordReaperExpiredFailure(string) {}

func NewReaper(transitionStore store.TaskTransitionStore) *Reaper {
	return &Reaper{
		transitionStore: transitionStore,
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics:         noopReaperMetricsRecorder{},
		now:             time.Now,
	}
}

func (r *Reaper) WithLogger(logger *slog.Logger) *Reaper {
	if logger != nil {
		r.logger = logger
	}
	return r
}

func (r *Reaper) WithMetrics(metrics ReaperMetricsRecorder) *Reaper {
	if metrics != nil {
		r.metrics = metrics
	}
	return r
}

func (r *Reaper) ProcessNext(ctx context.Context) (bool, error) {
	task, err := r.transitionStore.FailNextExpiredWithEvent(
		ctx,
		r.now(),
		identity.New("event"),
	)
	if errors.Is(err, store.ErrNoExpiredTask) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("fail expired task and append event: %w", err)
	}
	r.metrics.RecordReaperExpiredFailure(task.Workflow)
	r.logger.Warn(
		"expired task failed by reaper",
		"task_id", task.ID,
		"workflow", task.Workflow,
		"retry_count", task.RetryCount,
	)
	return true, nil
}

func (r *Reaper) Run(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		return errors.New("poll interval must be positive")
	}

	for {
		processed, err := r.ProcessNext(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
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
