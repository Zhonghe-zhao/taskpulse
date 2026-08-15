package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zhonghe-zhao/taskpulse/internal/platform/database"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
	"github.com/Zhonghe-zhao/taskpulse/internal/store/mysqlstore"
)

const (
	storageBackendMemory = "memory"
	storageBackendMySQL  = "mysql"
)

type taskRuntimeStore interface {
	store.TaskTransitionStore
	store.TaskCancellationStore
}

type runtimeStores struct {
	tasks          store.TaskStore
	taskStats      store.TaskStatsStore
	events         store.EventStore
	taskCreation   store.TaskCreationStore
	taskTransition taskRuntimeStore
	close          func() error
}

func openRuntimeStores(ctx context.Context, backend string) (runtimeStores, error) {
	switch normalizeStorageBackend(backend) {
	case storageBackendMemory:
		taskStore := store.NewMemoryTaskStore()
		eventStore := store.NewMemoryEventStore()
		return runtimeStores{
			tasks:          taskStore,
			taskStats:      taskStore,
			events:         eventStore,
			taskCreation:   store.NewMemoryTaskCreationStore(taskStore, eventStore),
			taskTransition: store.NewMemoryTaskTransitionStore(taskStore, eventStore),
			close:          func() error { return nil },
		}, nil

	case storageBackendMySQL:
		config, err := database.MySQLConfigFromEnv()
		if err != nil {
			return runtimeStores{}, fmt.Errorf("load mysql config: %w", err)
		}
		db, err := database.OpenMySQL(ctx, config)
		if err != nil {
			return runtimeStores{}, fmt.Errorf("open mysql: %w", err)
		}

		taskStore, err := mysqlstore.NewTaskStore(db)
		if err != nil {
			_ = db.Close()
			return runtimeStores{}, err
		}
		eventStore, err := mysqlstore.NewEventStore(db)
		if err != nil {
			_ = db.Close()
			return runtimeStores{}, err
		}
		taskCreationStore, err := mysqlstore.NewTaskCreationStore(db)
		if err != nil {
			_ = db.Close()
			return runtimeStores{}, err
		}
		taskTransitionStore, err := mysqlstore.NewTaskTransitionStore(db)
		if err != nil {
			_ = db.Close()
			return runtimeStores{}, err
		}
		return runtimeStores{
			tasks:          taskStore,
			taskStats:      taskStore,
			events:         eventStore,
			taskCreation:   taskCreationStore,
			taskTransition: taskTransitionStore,
			close:          db.Close,
		}, nil

	default:
		return runtimeStores{}, errors.New("storage backend must be mysql or memory")
	}
}

func normalizeStorageBackend(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return storageBackendMySQL
	}
	return normalized
}
