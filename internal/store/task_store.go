package store

import (
	"context"
	"errors"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
)

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrTaskAlreadyExists   = errors.New("task already exists")
	ErrNilTask             = errors.New("task is nil")
	ErrNoTaskAvailable     = errors.New("no task available")
	ErrTaskConflict        = errors.New("task version conflict")
	ErrInvalidClaim        = errors.New("invalid task claim")
	ErrInvalidLeaseRenewal = errors.New("invalid lease renewal")
	ErrLeaseLost           = errors.New("task lease lost")
	ErrNoExpiredTask       = errors.New("no expired task awaiting cleanup")
	ErrInvalidCleanupTime  = errors.New("invalid task cleanup time")
	ErrInvalidEventID      = errors.New("event id is required")
)

type ClaimOptions struct {
	WorkerID      string
	Workflow      string
	Now           time.Time
	LeaseDuration time.Duration
}

func (o ClaimOptions) Validate() error {
	if o.WorkerID == "" || o.Now.IsZero() || o.LeaseDuration <= 0 {
		return ErrInvalidClaim
	}
	return nil
}

type RenewLeaseOptions struct {
	TaskID        string
	WorkerID      string
	Now           time.Time
	LeaseDuration time.Duration
}

func (o RenewLeaseOptions) Validate() error {
	if o.TaskID == "" || o.WorkerID == "" || o.Now.IsZero() || o.LeaseDuration <= 0 {
		return ErrInvalidLeaseRenewal
	}
	return nil
}

type TaskStore interface {
	Create(ctx context.Context, task *domain.Task) error
	Get(ctx context.Context, id string) (*domain.Task, error)
	Update(ctx context.Context, task *domain.Task) error
	ClaimNext(ctx context.Context, options ClaimOptions) (*domain.Task, error)
	RenewLease(ctx context.Context, options RenewLeaseOptions) error
	FailNextExpired(ctx context.Context, now time.Time) (*domain.Task, error)
}
