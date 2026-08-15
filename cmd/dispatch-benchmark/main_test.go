package main

import "testing"

func TestParseMetricsReadsClaimOutcomeCounters(t *testing.T) {
	metrics := parseMetrics(`
taskpulse_claim_attempts_total{workflow="llm_analysis"} 10
taskpulse_claim_misses_total{workflow="llm_analysis"} 4
taskpulse_tasks_claimed_total{workflow="llm_analysis"} 6
taskpulse_tasks_current{status="queued"} 0
taskpulse_tasks_current{status="retrying"} 0
taskpulse_tasks_current{status="running"} 2
`, "llm_analysis")
	if !metrics.Available {
		t.Fatal("expected complete claim metrics")
	}
	if metrics.ClaimAttempts != 10 || metrics.ClaimMisses != 4 || metrics.TasksClaimed != 6 {
		t.Fatalf("unexpected claim metrics: %+v", metrics)
	}
	if !metrics.TaskStatsAvailable || metrics.ActiveTasks != 2 {
		t.Fatalf("unexpected task stats: %+v", metrics)
	}
}
