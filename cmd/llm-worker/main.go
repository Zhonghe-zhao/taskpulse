package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/executor/llmanalysis"
	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
	"github.com/Zhonghe-zhao/taskpulse/pkg/taskpulse"
	"github.com/Zhonghe-zhao/taskpulse/pkg/taskpulseworker"
)

type config struct {
	TaskPulseURL          string
	WorkerID              string
	Workflow              string
	Lease                 time.Duration
	PollInterval          time.Duration
	ClaimRetryMaxInterval time.Duration
	HeartbeatInterval     time.Duration
	ExecutionTimeout      time.Duration
	ShutdownTimeout       time.Duration
}

type llmExecutorAdapter struct {
	executor worker.Executor
}

func (a llmExecutorAdapter) Execute(
	ctx context.Context,
	task *taskpulse.Task,
	progress taskpulseworker.ProgressReporter,
) (taskpulseworker.Result, error) {
	internalTask := &domain.Task{
		ID:         task.ID,
		Workflow:   task.Workflow,
		Status:     domain.TaskStatus(task.Status),
		Input:      task.Input,
		RetryCount: task.RetryCount,
		MaxRetries: task.MaxRetries,
		Version:    task.Version,
	}
	if err := progress.Report(ctx, 10, "preparing task"); err != nil {
		return taskpulseworker.Result{}, err
	}
	result, err := a.executor.Execute(ctx, internalTask)
	if err != nil {
		if classified, ok := worker.AsExecutionError(err); ok {
			if classified.Retryable() {
				return taskpulseworker.Result{}, &taskpulseworker.Failure{
					Code: classified.Code, Message: classified.Error(), Retryable: true,
					RetryAfter: classified.RetryAfter, Err: err,
				}
			}
			return taskpulseworker.Result{}, taskpulseworker.Permanent(classified.Code, classified.Error(), err)
		}
		return taskpulseworker.Result{}, err
	}
	if err := progress.Report(ctx, 90, "validating result"); err != nil {
		return taskpulseworker.Result{}, err
	}
	return taskpulseworker.Result{Output: result.Output}, nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid worker configuration", "error", err)
		os.Exit(1)
	}

	fakeClient := llmanalysis.NewFakeClient("fake-llm")
	if err := fakeClient.SetFailureMode(os.Getenv("TASKPULSE_LLM_FAKE_FAILURE")); err != nil {
		logger.Error("configure fake llm client", "error", err)
		os.Exit(1)
	}
	if rawDelay := os.Getenv("TASKPULSE_LLM_FAKE_DELAY"); rawDelay != "" {
		delay, err := time.ParseDuration(rawDelay)
		if err != nil || delay < 0 {
			logger.Error("configure fake llm delay", "error", err)
			os.Exit(1)
		}
		if err := fakeClient.SetDelay(delay); err != nil {
			logger.Error("configure fake llm delay", "error", err)
			os.Exit(1)
		}
	}
	executor, err := llmanalysis.New(fakeClient)
	if err != nil {
		logger.Error("initialize llm executor", "error", err)
		os.Exit(1)
	}

	runtime, err := taskpulseworker.New(taskpulseworker.Config{
		Client:                &taskpulse.Client{BaseURL: cfg.TaskPulseURL, HTTPClient: &http.Client{Timeout: 10 * time.Second}},
		WorkerID:              cfg.WorkerID,
		Workflow:              cfg.Workflow,
		LeaseDuration:         cfg.Lease,
		PollInterval:          cfg.PollInterval,
		ClaimRetryMaxInterval: cfg.ClaimRetryMaxInterval,
		HeartbeatInterval:     cfg.HeartbeatInterval,
		ExecutionTimeout:      cfg.ExecutionTimeout,
		ShutdownTimeout:       cfg.ShutdownTimeout,
		Logger:                logger,
	})
	if err != nil {
		logger.Error("initialize taskpulse runtime", "error", err)
		os.Exit(1)
	}
	if err := runtime.Register(cfg.Workflow, llmExecutorAdapter{executor: executor}); err != nil {
		logger.Error("register llm executor", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("external llm worker started", "worker_id", cfg.WorkerID, "workflow", cfg.Workflow, "taskpulse_url", cfg.TaskPulseURL)
	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("external worker stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("external llm worker stopped", "worker_id", cfg.WorkerID)
}

func loadConfig() (config, error) {
	lease, err := parseDurationEnv("TASKPULSE_EXTERNAL_LEASE", 30*time.Second)
	if err != nil {
		return config{}, err
	}
	poll, err := parseDurationEnv("TASKPULSE_EXTERNAL_POLL_INTERVAL", 200*time.Millisecond)
	if err != nil {
		return config{}, err
	}
	claimRetryMaxInterval, err := parseDurationEnv("TASKPULSE_EXTERNAL_CLAIM_RETRY_MAX_INTERVAL", 5*time.Second)
	if err != nil {
		return config{}, err
	}
	heartbeat, err := parseDurationEnv("TASKPULSE_EXTERNAL_HEARTBEAT_INTERVAL", lease/3)
	if err != nil {
		return config{}, err
	}
	executionTimeout, err := parseOptionalDurationEnv("TASKPULSE_EXTERNAL_EXECUTION_TIMEOUT")
	if err != nil {
		return config{}, err
	}
	shutdownTimeout, err := parseDurationEnv("TASKPULSE_EXTERNAL_SHUTDOWN_TIMEOUT", 5*time.Second)
	if err != nil {
		return config{}, err
	}
	workerID := os.Getenv("TASKPULSE_EXTERNAL_WORKER_ID")
	if workerID == "" {
		workerID = defaultWorkerID()
	}
	return config{
		TaskPulseURL:          strings.TrimRight(defaultEnv("TASKPULSE_URL", "http://localhost:8080"), "/"),
		WorkerID:              workerID,
		Workflow:              defaultEnv("TASKPULSE_WORKER_WORKFLOW", "llm_analysis"),
		Lease:                 lease,
		PollInterval:          poll,
		ClaimRetryMaxInterval: claimRetryMaxInterval,
		HeartbeatInterval:     heartbeat,
		ExecutionTimeout:      executionTimeout,
		ShutdownTimeout:       shutdownTimeout,
	}, nil
}

func defaultWorkerID() string {
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		return "external-llm-worker-" + hostname
	}
	return "external-llm-worker"
}

func parseDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

func parseOptionalDurationEnv(name string) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration when set", name)
	}
	return duration, nil
}

func defaultEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
