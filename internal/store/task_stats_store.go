package store

import (
	"context"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

type TaskStatsStore interface {
	SnapshotTaskStats(ctx context.Context, now time.Time) (*TaskStatsSnapshot, error)
}

type TaskStatsFilter struct {
	Workflow string
	Status   domain.TaskStatus
}

func (f TaskStatsFilter) Validate() error {
	if f.Status != "" && !isKnownTaskStatus(f.Status) {
		return ErrInvalidTaskQuery
	}
	return nil
}

type FilteredTaskStatsStore interface {
	SnapshotFilteredTaskStats(ctx context.Context, now time.Time, filter TaskStatsFilter) (*TaskStatsSnapshot, error)
}

type TaskStatsSnapshot struct {
	StatusCounts       map[domain.TaskStatus]int
	AvailableCounts    map[domain.TaskStatus]int
	OldestAvailableAge map[domain.TaskStatus]time.Duration
}

func NewTaskStatsSnapshot() *TaskStatsSnapshot {
	return &TaskStatsSnapshot{
		StatusCounts:       make(map[domain.TaskStatus]int),
		AvailableCounts:    make(map[domain.TaskStatus]int),
		OldestAvailableAge: make(map[domain.TaskStatus]time.Duration),
	}
}

func (s *MemoryTaskStore) SnapshotTaskStats(ctx context.Context, now time.Time) (*TaskStatsSnapshot, error) {
	return s.SnapshotFilteredTaskStats(ctx, now, TaskStatsFilter{})
}

func (s *MemoryTaskStore) SnapshotFilteredTaskStats(
	ctx context.Context,
	now time.Time,
	filter TaskStatsFilter,
) (*TaskStatsSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := NewTaskStatsSnapshot()
	for _, task := range s.tasks {
		if filter.Workflow != "" && task.Workflow != filter.Workflow {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		snapshot.StatusCounts[task.Status]++
		if isAvailableTask(task, now) {
			snapshot.AvailableCounts[task.Status]++
			age := now.Sub(task.AvailableAt)
			if previous, exists := snapshot.OldestAvailableAge[task.Status]; !exists || age > previous {
				snapshot.OldestAvailableAge[task.Status] = age
			}
		}
	}
	return snapshot, nil
}

func isAvailableTask(task *domain.Task, now time.Time) bool {
	if task == nil {
		return false
	}
	if task.Status != domain.TaskStatusQueued && task.Status != domain.TaskStatusRetrying {
		return false
	}
	return !task.AvailableAt.After(now)
}
