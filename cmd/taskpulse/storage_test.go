package main

import (
	"context"
	"testing"

	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestOpenRuntimeStoresUsesMemoryWhenExplicitlyConfigured(t *testing.T) {
	stores, err := openRuntimeStores(context.Background(), storageBackendMemory)
	if err != nil {
		t.Fatalf("openRuntimeStores returned error: %v", err)
	}
	defer stores.close()

	if _, ok := stores.tasks.(*store.MemoryTaskStore); !ok {
		t.Fatalf("expected MemoryTaskStore, got %T", stores.tasks)
	}
	if _, ok := stores.events.(*store.MemoryEventStore); !ok {
		t.Fatalf("expected MemoryEventStore, got %T", stores.events)
	}
}

func TestNormalizeStorageBackendDefaultsToMySQL(t *testing.T) {
	if got := normalizeStorageBackend(""); got != storageBackendMySQL {
		t.Fatalf("expected default backend %q, got %q", storageBackendMySQL, got)
	}
	if got := normalizeStorageBackend(" MySQL "); got != storageBackendMySQL {
		t.Fatalf("expected normalized backend %q, got %q", storageBackendMySQL, got)
	}
}

func TestOpenRuntimeStoresRejectsUnknownBackend(t *testing.T) {
	if _, err := openRuntimeStores(context.Background(), "redis"); err == nil {
		t.Fatal("expected unknown backend to be rejected")
	}
}
