package store

import (
	"context"
	"errors"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

var (
	ErrEventNotFound      = errors.New("event not found")
	ErrEventAlreadyExists = errors.New("event already exists")
	ErrNilEvent           = errors.New("event is nil")
)

type EventStore interface {
	Append(ctx context.Context, event *domain.TaskEvent) error
	ListByTaskID(ctx context.Context, taskID string) ([]*domain.TaskEvent, error)
}
