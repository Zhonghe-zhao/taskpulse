package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

type fakeTaskStatsStore struct {
	snapshot *store.TaskStatsSnapshot
}

func (s fakeTaskStatsStore) SnapshotTaskStats(context.Context, time.Time) (*store.TaskStatsSnapshot, error) {
	return s.snapshot, nil
}

func TestMetricsServePrometheusText(t *testing.T) {
	snapshot := store.NewTaskStatsSnapshot()
	snapshot.StatusCounts[domain.TaskStatusQueued] = 2
	snapshot.StatusCounts[domain.TaskStatusRunning] = 1
	snapshot.AvailableCounts[domain.TaskStatusQueued] = 2
	snapshot.OldestAvailableAge[domain.TaskStatusQueued] = 30 * time.Second

	metrics := NewMetrics().WithTaskStatsStore(fakeTaskStatsStore{snapshot: snapshot})
	metrics.RecordClaimAttempt("llm_analysis")
	metrics.RecordClaimAttempt("llm_analysis")
	metrics.RecordClaimMiss("llm_analysis")
	metrics.RecordTaskClaimed("llm_analysis")
	metrics.RecordTaskReleased("llm_analysis")
	metrics.RecordTaskRetried("llm_analysis", "llm_rate_limited")
	metrics.RecordTaskCompleted("llm_analysis", domain.TaskStatusSucceeded, 150*time.Millisecond)

	response := httptest.NewRecorder()
	metrics.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()

	for _, want := range []string{
		`taskpulse_claim_attempts_total{workflow="llm_analysis"} 2`,
		`taskpulse_claim_misses_total{workflow="llm_analysis"} 1`,
		`taskpulse_tasks_claimed_total{workflow="llm_analysis"} 1`,
		`taskpulse_tasks_released_total{workflow="llm_analysis"} 1`,
		`taskpulse_tasks_retried_total{workflow="llm_analysis",error_code="llm_rate_limited"} 1`,
		`taskpulse_tasks_completed_total{workflow="llm_analysis",status="succeeded"} 1`,
		`taskpulse_task_execution_duration_seconds_count{workflow="llm_analysis"} 1`,
		`taskpulse_tasks_current{status="queued"} 2`,
		`taskpulse_tasks_current{status="running"} 1`,
		`taskpulse_tasks_available_current{status="queued"} 2`,
		`taskpulse_oldest_available_task_age_seconds{status="queued"} 30`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}
