package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

var defaultDurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

var taskStatuses = []domain.TaskStatus{
	domain.TaskStatusQueued,
	domain.TaskStatusRunning,
	domain.TaskStatusRetrying,
	domain.TaskStatusSucceeded,
	domain.TaskStatusPartial,
	domain.TaskStatusFailed,
	domain.TaskStatusCanceled,
}

type Metrics struct {
	mu sync.Mutex

	taskStatsStore  store.TaskStatsStore
	claimAttempts   map[string]uint64
	claimMisses     map[string]uint64
	tasksClaimed    map[string]uint64
	tasksReleased   map[string]uint64
	tasksCompleted  map[completionKey]uint64
	tasksRetried    map[retryKey]uint64
	leaseRenewed    map[string]uint64
	leaseLost       map[string]uint64
	reaperExpired   map[string]uint64
	executionHist   map[string]*histogram
	durationBuckets []float64
}

type completionKey struct {
	workflow string
	status   domain.TaskStatus
}

type retryKey struct {
	workflow  string
	errorCode string
}

type histogram struct {
	buckets []uint64
	sum     float64
	count   uint64
}

func NewMetrics() *Metrics {
	return &Metrics{
		claimAttempts:   make(map[string]uint64),
		claimMisses:     make(map[string]uint64),
		tasksClaimed:    make(map[string]uint64),
		tasksReleased:   make(map[string]uint64),
		tasksCompleted:  make(map[completionKey]uint64),
		tasksRetried:    make(map[retryKey]uint64),
		leaseRenewed:    make(map[string]uint64),
		leaseLost:       make(map[string]uint64),
		reaperExpired:   make(map[string]uint64),
		executionHist:   make(map[string]*histogram),
		durationBuckets: append([]float64(nil), defaultDurationBuckets...),
	}
}

func (m *Metrics) WithTaskStatsStore(taskStatsStore store.TaskStatsStore) *Metrics {
	m.taskStatsStore = taskStatsStore
	return m
}

func (m *Metrics) RecordClaimAttempt(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimAttempts[normalizeWorkflow(workflow)]++
}

func (m *Metrics) RecordClaimMiss(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimMisses[normalizeWorkflow(workflow)]++
}

func normalizeWorkflow(workflow string) string {
	if workflow == "" {
		return "all"
	}
	return workflow
}

func (m *Metrics) RecordTaskClaimed(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasksClaimed[workflow]++
}

func (m *Metrics) RecordTaskReleased(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasksReleased[workflow]++
}

func (m *Metrics) RecordTaskCompleted(workflow string, status domain.TaskStatus, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasksCompleted[completionKey{workflow: workflow, status: status}]++
	m.observeExecutionDurationLocked(workflow, duration)
}

func (m *Metrics) RecordTaskRetried(workflow string, errorCode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasksRetried[retryKey{workflow: workflow, errorCode: errorCode}]++
}

func (m *Metrics) RecordLeaseRenewed(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leaseRenewed[workflow]++
}

func (m *Metrics) RecordLeaseLost(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leaseLost[workflow]++
}

func (m *Metrics) RecordReaperExpiredFailure(workflow string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reaperExpired[workflow]++
}

func (m *Metrics) observeExecutionDurationLocked(workflow string, duration time.Duration) {
	seconds := duration.Seconds()
	hist, exists := m.executionHist[workflow]
	if !exists {
		hist = &histogram{buckets: make([]uint64, len(m.durationBuckets))}
		m.executionHist[workflow] = hist
	}
	for index, upperBound := range m.durationBuckets {
		if seconds <= upperBound {
			hist.buckets[index]++
			break
		}
	}
	hist.sum += seconds
	hist.count++
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var snapshot *store.TaskStatsSnapshot
	if m.taskStatsStore != nil {
		var err error
		snapshot, err = m.taskStatsStore.SnapshotTaskStats(r.Context(), time.Now())
		if err != nil {
			http.Error(w, fmt.Sprintf("snapshot task stats: %v", err), http.StatusInternalServerError)
			return
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if snapshot != nil {
		writeTaskStats(w, snapshot)
	}

	writeHelp(w, "taskpulse_claim_attempts_total", "Total task claim attempts by requested workflow.")
	writeType(w, "taskpulse_claim_attempts_total", "counter")
	for _, workflow := range sortedStringKeys(m.claimAttempts) {
		fmt.Fprintf(w, "taskpulse_claim_attempts_total{workflow=%q} %d\n", workflow, m.claimAttempts[workflow])
	}

	writeHelp(w, "taskpulse_claim_misses_total", "Total task claim attempts with no available task by requested workflow.")
	writeType(w, "taskpulse_claim_misses_total", "counter")
	for _, workflow := range sortedStringKeys(m.claimMisses) {
		fmt.Fprintf(w, "taskpulse_claim_misses_total{workflow=%q} %d\n", workflow, m.claimMisses[workflow])
	}

	writeHelp(w, "taskpulse_tasks_claimed_total", "Total tasks claimed by workers.")
	writeType(w, "taskpulse_tasks_claimed_total", "counter")
	for _, workflow := range sortedStringKeys(m.tasksClaimed) {
		fmt.Fprintf(w, "taskpulse_tasks_claimed_total{workflow=%q} %d\n", workflow, m.tasksClaimed[workflow])
	}

	writeHelp(w, "taskpulse_tasks_released_total", "Total running tasks returned to the queue by their Worker during graceful shutdown.")
	writeType(w, "taskpulse_tasks_released_total", "counter")
	for _, workflow := range sortedStringKeys(m.tasksReleased) {
		fmt.Fprintf(w, "taskpulse_tasks_released_total{workflow=%q} %d\n", workflow, m.tasksReleased[workflow])
	}

	writeHelp(w, "taskpulse_tasks_completed_total", "Total tasks completed by terminal status.")
	writeType(w, "taskpulse_tasks_completed_total", "counter")
	for _, key := range sortedCompletionKeys(m.tasksCompleted) {
		fmt.Fprintf(
			w,
			"taskpulse_tasks_completed_total{workflow=%q,status=%q} %d\n",
			key.workflow,
			key.status,
			m.tasksCompleted[key],
		)
	}

	writeHelp(w, "taskpulse_tasks_retried_total", "Total tasks scheduled for retry.")
	writeType(w, "taskpulse_tasks_retried_total", "counter")
	for _, key := range sortedRetryKeys(m.tasksRetried) {
		fmt.Fprintf(
			w,
			"taskpulse_tasks_retried_total{workflow=%q,error_code=%q} %d\n",
			key.workflow,
			key.errorCode,
			m.tasksRetried[key],
		)
	}

	writeHelp(w, "taskpulse_lease_renewed_total", "Total successful lease renewals.")
	writeType(w, "taskpulse_lease_renewed_total", "counter")
	for _, workflow := range sortedStringKeys(m.leaseRenewed) {
		fmt.Fprintf(w, "taskpulse_lease_renewed_total{workflow=%q} %d\n", workflow, m.leaseRenewed[workflow])
	}

	writeHelp(w, "taskpulse_lease_lost_total", "Total lease loss events observed by workers.")
	writeType(w, "taskpulse_lease_lost_total", "counter")
	for _, workflow := range sortedStringKeys(m.leaseLost) {
		fmt.Fprintf(w, "taskpulse_lease_lost_total{workflow=%q} %d\n", workflow, m.leaseLost[workflow])
	}

	writeHelp(w, "taskpulse_reaper_expired_failures_total", "Total expired tasks failed by the reaper.")
	writeType(w, "taskpulse_reaper_expired_failures_total", "counter")
	for _, workflow := range sortedStringKeys(m.reaperExpired) {
		fmt.Fprintf(w, "taskpulse_reaper_expired_failures_total{workflow=%q} %d\n", workflow, m.reaperExpired[workflow])
	}

	writeHelp(w, "taskpulse_task_execution_duration_seconds", "Task execution duration in seconds.")
	writeType(w, "taskpulse_task_execution_duration_seconds", "histogram")
	for _, workflow := range sortedHistogramKeys(m.executionHist) {
		hist := m.executionHist[workflow]
		var cumulative uint64
		for index, upperBound := range m.durationBuckets {
			cumulative += hist.buckets[index]
			fmt.Fprintf(
				w,
				"taskpulse_task_execution_duration_seconds_bucket{workflow=%q,le=%q} %d\n",
				workflow,
				formatBucket(upperBound),
				cumulative,
			)
		}
		fmt.Fprintf(w, "taskpulse_task_execution_duration_seconds_bucket{workflow=%q,le=\"+Inf\"} %d\n", workflow, hist.count)
		fmt.Fprintf(w, "taskpulse_task_execution_duration_seconds_sum{workflow=%q} %g\n", workflow, hist.sum)
		fmt.Fprintf(w, "taskpulse_task_execution_duration_seconds_count{workflow=%q} %d\n", workflow, hist.count)
	}
}

func writeTaskStats(w http.ResponseWriter, snapshot *store.TaskStatsSnapshot) {
	writeHelp(w, "taskpulse_tasks_current", "Current number of tasks by status.")
	writeType(w, "taskpulse_tasks_current", "gauge")
	for _, status := range taskStatuses {
		fmt.Fprintf(w, "taskpulse_tasks_current{status=%q} %d\n", status, snapshot.StatusCounts[status])
	}

	writeHelp(w, "taskpulse_tasks_available_current", "Current number of queued or retrying tasks available for claim.")
	writeType(w, "taskpulse_tasks_available_current", "gauge")
	for _, status := range []domain.TaskStatus{domain.TaskStatusQueued, domain.TaskStatusRetrying} {
		fmt.Fprintf(w, "taskpulse_tasks_available_current{status=%q} %d\n", status, snapshot.AvailableCounts[status])
	}

	writeHelp(w, "taskpulse_oldest_available_task_age_seconds", "Age of the oldest queued or retrying task available for claim.")
	writeType(w, "taskpulse_oldest_available_task_age_seconds", "gauge")
	for _, status := range []domain.TaskStatus{domain.TaskStatusQueued, domain.TaskStatusRetrying} {
		fmt.Fprintf(
			w,
			"taskpulse_oldest_available_task_age_seconds{status=%q} %g\n",
			status,
			snapshot.OldestAvailableAge[status].Seconds(),
		)
	}
}

func writeHelp(w http.ResponseWriter, name string, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
}

func writeType(w http.ResponseWriter, name string, metricType string) {
	fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)
}

func sortedStringKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedCompletionKeys(values map[completionKey]uint64) []completionKey {
	keys := make([]completionKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := keys[i].workflow + "\x00" + string(keys[i].status)
		right := keys[j].workflow + "\x00" + string(keys[j].status)
		return left < right
	})
	return keys
}

func sortedRetryKeys(values map[retryKey]uint64) []retryKey {
	keys := make([]retryKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := keys[i].workflow + "\x00" + keys[i].errorCode
		right := keys[j].workflow + "\x00" + keys[j].errorCode
		return left < right
	})
	return keys
}

func sortedHistogramKeys(values map[string]*histogram) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatBucket(bucket float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", bucket), "0"), ".")
}
