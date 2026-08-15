package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zhaozhonghe/taskpulse/pkg/taskpulse"
)

type config struct {
	baseURL           string
	workflow          string
	tasks             int
	createWorkers     int
	statusWorkers     int
	pollInterval      time.Duration
	timeout           time.Duration
	maxRetries        int
	metricsEndpoint   string
	outputPath        string
	requireCleanStart bool
}

type metricSnapshot struct {
	Available          bool
	ClaimAttempts      uint64
	ClaimMisses        uint64
	TasksClaimed       uint64
	TaskStatsAvailable bool
	ActiveTasks        uint64
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.baseURL, "base-url", "http://127.0.0.1:8085", "TaskPulse HTTP base URL")
	flag.StringVar(&cfg.workflow, "workflow", "llm_analysis", "workflow to benchmark")
	flag.IntVar(&cfg.tasks, "tasks", 1000, "number of tasks")
	flag.IntVar(&cfg.createWorkers, "create-workers", 16, "concurrent task creation workers")
	flag.IntVar(&cfg.statusWorkers, "status-workers", 32, "bounded concurrent task status requests")
	flag.DurationVar(&cfg.pollInterval, "status-poll-interval", 250*time.Millisecond, "task status observation polling interval")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 250*time.Millisecond, "deprecated alias for --status-poll-interval")
	flag.DurationVar(&cfg.timeout, "timeout", 15*time.Minute, "benchmark timeout")
	flag.IntVar(&cfg.maxRetries, "max-retries", 0, "maximum retries per task")
	flag.StringVar(&cfg.metricsEndpoint, "metrics", "/metrics", "metrics endpoint path")
	flag.StringVar(&cfg.outputPath, "output", "", "optional JSON report path")
	flag.BoolVar(&cfg.requireCleanStart, "require-clean-start", true, "require no queued, retrying, or running tasks before the benchmark")
	flag.Parse()

	if cfg.tasks <= 0 || cfg.createWorkers <= 0 || cfg.statusWorkers <= 0 || cfg.pollInterval <= 0 || cfg.timeout <= 0 {
		fatal("tasks, create-workers, status-workers, poll-interval and timeout must be positive")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	client := &taskpulse.Client{
		BaseURL:    cfg.baseURL,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}

	beforeMetrics := readMetrics(ctx, client, cfg.metricsEndpoint, cfg.workflow)
	if cfg.requireCleanStart && (!beforeMetrics.TaskStatsAvailable || beforeMetrics.ActiveTasks != 0) {
		fatal("benchmark requires a clean queue; task_stats_available=%t active_tasks=%d", beforeMetrics.TaskStatsAvailable, beforeMetrics.ActiveTasks)
	}
	benchmarkStartedAt := time.Now()
	created, createErrors := createTasks(ctx, client, cfg)
	if len(created) == 0 {
		fatal("no task was created; create_errors=%d", createErrors)
	}
	if createErrors > 0 {
		cancelCreatedTasks(ctx, client, created)
		fatal("task creation was incomplete; requested=%d created=%d create_errors=%d", cfg.tasks, len(created), createErrors)
	}
	fmt.Printf("created=%d create_errors=%d first_task=%s\n", len(created), createErrors, created[0].ID)

	result := waitForTasks(ctx, client, created, cfg.statusWorkers, cfg.pollInterval)
	result.Elapsed = time.Since(benchmarkStartedAt)
	afterMetrics := readMetrics(ctx, client, cfg.metricsEndpoint, cfg.workflow)
	report := buildReport(cfg, len(created), createErrors, result, beforeMetrics, afterMetrics)
	printReport(report)
	if cfg.outputPath != "" {
		if err := writeReport(cfg.outputPath, report); err != nil {
			fatal("write benchmark report: %v", err)
		}
		fmt.Printf("report=%s\n", cfg.outputPath)
	}
	if result.Unfinished > 0 {
		fatal("benchmark timed out with unfinished=%d", result.Unfinished)
	}
}

func cancelCreatedTasks(ctx context.Context, client *taskpulse.Client, tasks []taskpulse.Task) {
	for _, task := range tasks {
		_, _ = client.CancelTask(ctx, task.ID)
	}
}

func createTasks(ctx context.Context, client *taskpulse.Client, cfg config) ([]taskpulse.Task, int) {
	created := make([]taskpulse.Task, cfg.tasks)
	var next atomic.Int64
	var createErrors atomic.Int64
	var waitGroup sync.WaitGroup
	for worker := 0; worker < cfg.createWorkers; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for {
				index := int(next.Add(1)) - 1
				if index >= cfg.tasks {
					return
				}
				input := []byte(fmt.Sprintf(
					`{"subject":"dispatch-benchmark","goal":"measure task dispatch","notes":["sequence-%d"]}`,
					index,
				))
				item, err := client.CreateTask(ctx, taskpulse.CreateTaskRequest{
					Workflow:       cfg.workflow,
					Input:          input,
					MaxRetries:     cfg.maxRetries,
					IdempotencyKey: fmt.Sprintf("dispatch-benchmark:%d:%d", time.Now().UnixNano(), index),
				})
				if err != nil {
					createErrors.Add(1)
					continue
				}
				created[index] = *item
			}
		}()
	}
	waitGroup.Wait()
	compact := created[:0]
	for _, item := range created {
		if item.ID != "" {
			compact = append(compact, item)
		}
	}
	return compact, int(createErrors.Load())
}

type benchmarkResult struct {
	Started    []time.Duration
	Completed  []time.Duration
	Failed     int
	Unfinished int
	Elapsed    time.Duration
}

type latencySummary struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type benchmarkReport struct {
	GeneratedAt             time.Time      `json:"generated_at"`
	BaseURL                 string         `json:"base_url"`
	Workflow                string         `json:"workflow"`
	Requested               int            `json:"requested_tasks"`
	Created                 int            `json:"created_tasks"`
	CreateErrors            int            `json:"create_errors"`
	WorkerCreateConcurrency int            `json:"worker_create_concurrency"`
	StatusPollConcurrency   int            `json:"status_poll_concurrency"`
	PollIntervalMs          float64        `json:"poll_interval_ms"`
	Completed               int            `json:"completed_tasks"`
	Failed                  int            `json:"failed_tasks"`
	Unfinished              int            `json:"unfinished_tasks"`
	DurationMs              float64        `json:"duration_ms"`
	ThroughputPerSecond     float64        `json:"throughput_per_second"`
	QueueWait               latencySummary `json:"queue_wait"`
	TotalTime               latencySummary `json:"total_time"`
	ClaimAttempts           uint64         `json:"claim_attempts"`
	ClaimMisses             uint64         `json:"claim_misses"`
	TasksClaimed            uint64         `json:"tasks_claimed"`
	ClaimMetricsAvailable   bool           `json:"claim_metrics_available"`
	EmptyClaimRatio         *float64       `json:"empty_claim_ratio,omitempty"`
}

func waitForTasks(
	ctx context.Context,
	client *taskpulse.Client,
	tasks []taskpulse.Task,
	statusWorkers int,
	pollInterval time.Duration,
) benchmarkResult {
	result := benchmarkResult{}
	pending := make(map[string]taskpulse.Task, len(tasks))
	started := make(map[string]bool, len(tasks))
	for _, item := range tasks {
		pending[item.ID] = item
	}

	for len(pending) > 0 {
		batch := make([]taskpulse.Task, 0, len(pending))
		for _, task := range pending {
			batch = append(batch, task)
		}
		for _, observation := range observeTasks(ctx, client, batch, statusWorkers) {
			if observation.err != nil {
				continue
			}
			current := observation.task
			original := pending[observation.id]
			if !started[observation.id] && current.StartedAt != nil {
				result.Started = append(result.Started, current.StartedAt.Sub(original.CreatedAt))
				started[observation.id] = true
			}
			if current.FinishedAt == nil {
				continue
			}
			result.Completed = append(result.Completed, current.FinishedAt.Sub(original.CreatedAt))
			if current.Status != "succeeded" {
				result.Failed++
			}
			delete(pending, observation.id)
		}
		if len(pending) == 0 {
			break
		}
		if err := wait(ctx, pollInterval); err != nil {
			result.Unfinished = len(pending)
			return result
		}
	}
	return result
}

type taskObservation struct {
	id   string
	task *taskpulse.Task
	err  error
}

func observeTasks(ctx context.Context, client *taskpulse.Client, tasks []taskpulse.Task, workers int) []taskObservation {
	if workers > len(tasks) {
		workers = len(tasks)
	}
	if workers == 0 {
		return nil
	}
	jobs := make(chan taskpulse.Task)
	observations := make(chan taskObservation, len(tasks))
	var waitGroup sync.WaitGroup
	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for task := range jobs {
				current, err := client.GetTask(ctx, task.ID)
				observations <- taskObservation{id: task.ID, task: current, err: err}
			}
		}()
	}
	for _, task := range tasks {
		jobs <- task
	}
	close(jobs)
	waitGroup.Wait()
	close(observations)
	result := make([]taskObservation, 0, len(tasks))
	for observation := range observations {
		result = append(result, observation)
	}
	return result
}

func buildReport(cfg config, created, createErrors int, result benchmarkResult, before, after metricSnapshot) benchmarkReport {
	report := benchmarkReport{
		GeneratedAt: time.Now().UTC(),
		BaseURL:     cfg.baseURL, Workflow: cfg.workflow,
		Requested: cfg.tasks, Created: created, CreateErrors: createErrors,
		WorkerCreateConcurrency: cfg.createWorkers,
		StatusPollConcurrency:   cfg.statusWorkers,
		PollIntervalMs:          float64(cfg.pollInterval) / float64(time.Millisecond),
		Completed:               len(result.Completed), Failed: result.Failed, Unfinished: result.Unfinished,
		QueueWait:             summarizeLatency(result.Started),
		TotalTime:             summarizeLatency(result.Completed),
		ClaimMetricsAvailable: before.Available && after.Available,
	}
	if report.ClaimMetricsAvailable && after.ClaimAttempts >= before.ClaimAttempts && after.ClaimMisses >= before.ClaimMisses && after.TasksClaimed >= before.TasksClaimed {
		report.ClaimAttempts = after.ClaimAttempts - before.ClaimAttempts
		report.ClaimMisses = after.ClaimMisses - before.ClaimMisses
		report.TasksClaimed = after.TasksClaimed - before.TasksClaimed
	} else {
		report.ClaimMetricsAvailable = false
	}
	if len(result.Completed) > 0 && result.Elapsed > 0 {
		duration := result.Elapsed
		report.DurationMs = float64(duration) / float64(time.Millisecond)
		if duration > 0 {
			report.ThroughputPerSecond = float64(len(result.Completed)) / duration.Seconds()
		}
	}
	if report.ClaimMetricsAvailable && report.ClaimAttempts > 0 {
		ratio := float64(report.ClaimMisses) / float64(report.ClaimAttempts)
		report.EmptyClaimRatio = &ratio
	}
	return report
}

func summarizeLatency(values []time.Duration) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	copyOfValues := append([]time.Duration(nil), values...)
	sort.Slice(copyOfValues, func(i, j int) bool { return copyOfValues[i] < copyOfValues[j] })
	return latencySummary{
		Samples: len(copyOfValues),
		P50Ms:   durationMilliseconds(percentile(copyOfValues, 0.50)),
		P95Ms:   durationMilliseconds(percentile(copyOfValues, 0.95)),
		P99Ms:   durationMilliseconds(percentile(copyOfValues, 0.99)),
		MaxMs:   durationMilliseconds(maxDuration(copyOfValues)),
	}
}

func durationMilliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func printReport(report benchmarkReport) {
	fmt.Printf("completed=%d failed=%d unfinished=%d duration_seconds=%.3f throughput=%.3f/s\n",
		report.Completed, report.Failed, report.Unfinished,
		report.DurationMs/1000, report.ThroughputPerSecond)
	fmt.Printf("queue_wait_p50_ms=%.3f queue_wait_p95_ms=%.3f queue_wait_p99_ms=%.3f queue_wait_max_ms=%.3f\n",
		report.QueueWait.P50Ms, report.QueueWait.P95Ms, report.QueueWait.P99Ms, report.QueueWait.MaxMs)
	fmt.Printf("total_time_p50_ms=%.3f total_time_p95_ms=%.3f total_time_p99_ms=%.3f total_time_max_ms=%.3f\n",
		report.TotalTime.P50Ms, report.TotalTime.P95Ms, report.TotalTime.P99Ms, report.TotalTime.MaxMs)
	if report.ClaimMetricsAvailable && report.EmptyClaimRatio != nil {
		fmt.Printf("claim_attempts=%d tasks_claimed=%d claim_misses=%d empty_claim_ratio=%.4f\n",
			report.ClaimAttempts, report.TasksClaimed, report.ClaimMisses, *report.EmptyClaimRatio)
		return
	}
	fmt.Printf("claim_attempts=unknown tasks_claimed=unknown claim_misses=unknown empty_claim_ratio=unknown\n")
}

func writeReport(path string, report benchmarkReport) error {
	directory := filepath.Dir(path)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func readMetrics(ctx context.Context, client *taskpulse.Client, endpoint, workflow string) metricSnapshot {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+endpoint, nil)
	if err != nil {
		return metricSnapshot{}
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return metricSnapshot{}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return metricSnapshot{}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return metricSnapshot{}
	}
	return parseMetrics(string(body), workflow)
}

func parseMetrics(body, workflow string) metricSnapshot {
	var result metricSnapshot
	var sawAttempts, sawMisses, sawClaimed bool
	activeStatuses := map[string]bool{"queued": false, "retrying": false, "running": false}
	for _, line := range splitLines(body) {
		if value, ok := parseLabeledCounter(line, "taskpulse_claim_attempts_total", "workflow", workflow); ok {
			result.ClaimAttempts = value
			sawAttempts = true
		}
		if value, ok := parseLabeledCounter(line, "taskpulse_claim_misses_total", "workflow", workflow); ok {
			result.ClaimMisses = value
			sawMisses = true
		}
		if value, ok := parseLabeledCounter(line, "taskpulse_tasks_claimed_total", "workflow", workflow); ok {
			result.TasksClaimed = value
			sawClaimed = true
		}
		for status := range activeStatuses {
			if value, ok := parseLabeledCounter(line, "taskpulse_tasks_current", "status", status); ok {
				result.ActiveTasks += value
				activeStatuses[status] = true
			}
		}
	}
	result.Available = sawAttempts && sawMisses && sawClaimed
	result.TaskStatsAvailable = activeStatuses["queued"] && activeStatuses["retrying"] && activeStatuses["running"]
	return result
}

func parseLabeledCounter(line, metric, label, value string) (uint64, bool) {
	prefix := metric + "{" + label + "=" + strconv.Quote(value) + "} "
	if !strings.HasPrefix(line, prefix) {
		return 0, false
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 10, 64)
	return parsed, err == nil
}

func splitLines(body string) []string {
	lines := make([]string, 0)
	for len(body) > 0 {
		index := 0
		for index < len(body) && body[index] != '\n' {
			index++
		}
		lines = append(lines, body[:index])
		if index == len(body) {
			break
		}
		body = body[index+1:]
	}
	return lines
}

func percentile(values []time.Duration, ratio float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*ratio)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func maxDuration(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1]
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fatal(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}
