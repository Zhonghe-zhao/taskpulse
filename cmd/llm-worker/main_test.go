package main

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultWorkerIDUsesProcessIdentity(t *testing.T) {
	id := defaultWorkerID()
	if !strings.HasPrefix(id, "external-llm-worker-") {
		t.Fatalf("expected hostname-derived worker ID, got %q", id)
	}
}

func TestLoadConfigReadsWorkerRuntimeIntervals(t *testing.T) {
	t.Setenv("TASKPULSE_EXTERNAL_LEASE", "30s")
	t.Setenv("TASKPULSE_EXTERNAL_POLL_INTERVAL", "200ms")
	t.Setenv("TASKPULSE_EXTERNAL_HEARTBEAT_INTERVAL", "10s")
	t.Setenv("TASKPULSE_EXTERNAL_CLAIM_RETRY_MAX_INTERVAL", "4s")
	t.Setenv("TASKPULSE_EXTERNAL_EXECUTION_TIMEOUT", "45s")
	t.Setenv("TASKPULSE_EXTERNAL_SHUTDOWN_TIMEOUT", "6s")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig returned error: %v", err)
	}
	if cfg.ClaimRetryMaxInterval != 4*time.Second {
		t.Fatalf("ClaimRetryMaxInterval = %s, want 4s", cfg.ClaimRetryMaxInterval)
	}
	if cfg.ShutdownTimeout != 6*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 6s", cfg.ShutdownTimeout)
	}
	if cfg.ExecutionTimeout != 45*time.Second {
		t.Fatalf("ExecutionTimeout = %s, want 45s", cfg.ExecutionTimeout)
	}
}
