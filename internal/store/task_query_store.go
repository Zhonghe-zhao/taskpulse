package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
)

var ErrInvalidTaskQuery = errors.New("invalid task query")

type ListTasksOptions struct {
	Workflow        string
	Status          domain.TaskStatus
	BeforeCreatedAt *time.Time
	BeforeID        string
	Limit           int
}

func (o ListTasksOptions) Validate() error {
	if o.Limit <= 0 || o.Limit > 101 {
		return ErrInvalidTaskQuery
	}
	if o.Status != "" && !isKnownTaskStatus(o.Status) {
		return ErrInvalidTaskQuery
	}
	if (o.BeforeCreatedAt == nil) != (o.BeforeID == "") {
		return ErrInvalidTaskQuery
	}
	return nil
}

type TaskListItem struct {
	ID             string            `json:"id"`
	Workflow       string            `json:"workflow"`
	Status         domain.TaskStatus `json:"status"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	Progress       int               `json:"progress"`
	RetryCount     int               `json:"retry_count"`
	MaxRetries     int               `json:"max_retries"`
	LeaseOwner     string            `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time        `json:"lease_expires_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type TaskQueryStore interface {
	ListTasks(ctx context.Context, options ListTasksOptions) ([]TaskListItem, error)
}

func (s *MemoryTaskStore) ListTasks(ctx context.Context, options ListTasksOptions) ([]TaskListItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	items := make([]TaskListItem, 0, len(s.tasks))
	for _, task := range s.tasks {
		if options.Workflow != "" && task.Workflow != options.Workflow {
			continue
		}
		if options.Status != "" && task.Status != options.Status {
			continue
		}
		if !isBeforeTaskCursor(task.CreatedAt, task.ID, options) {
			continue
		}
		items = append(items, newTaskListItem(task))
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > options.Limit {
		items = items[:options.Limit]
	}
	return items, nil
}

func isKnownTaskStatus(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskStatusQueued,
		domain.TaskStatusRunning,
		domain.TaskStatusSucceeded,
		domain.TaskStatusPartial,
		domain.TaskStatusFailed,
		domain.TaskStatusCanceled,
		domain.TaskStatusRetrying:
		return true
	default:
		return false
	}
}

func isBeforeTaskCursor(createdAt time.Time, id string, options ListTasksOptions) bool {
	if options.BeforeCreatedAt == nil {
		return true
	}
	return createdAt.Before(*options.BeforeCreatedAt) ||
		(createdAt.Equal(*options.BeforeCreatedAt) && id < options.BeforeID)
}

func newTaskListItem(task *domain.Task) TaskListItem {
	item := TaskListItem{
		ID:           task.ID,
		Workflow:     task.Workflow,
		Status:       task.Status,
		ErrorMessage: task.ErrorMessage,
		Progress:     task.Progress,
		RetryCount:   task.RetryCount,
		MaxRetries:   task.MaxRetries,
		LeaseOwner:   task.LeaseOwner,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
	if task.LeaseExpiresAt != nil {
		leaseExpiresAt := *task.LeaseExpiresAt
		item.LeaseExpiresAt = &leaseExpiresAt
	}
	return item
}
