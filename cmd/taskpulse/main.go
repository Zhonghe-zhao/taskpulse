package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	charmLog "github.com/charmbracelet/log"
	"github.com/zhaozhonghe/taskpulse/internal/application"
	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/executor/llmanalysis"
	"github.com/zhaozhonghe/taskpulse/internal/executor/urlcheck"
	"github.com/zhaozhonghe/taskpulse/internal/observability"
	"github.com/zhaozhonghe/taskpulse/internal/store"
	httptransport "github.com/zhaozhonghe/taskpulse/internal/transport/http"
	"github.com/zhaozhonghe/taskpulse/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backend := normalizeStorageBackend(os.Getenv("TASKPULSE_STORAGE"))
	stores, err := openRuntimeStores(ctx, backend)
	if err != nil {
		log.Fatalf("initialize %s storage: %v", backend, err)
	}
	defer func() {
		if err := stores.close(); err != nil {
			log.Printf("close %s storage: %v", backend, err)
		}
	}()
	log.Printf("TaskPulse storage backend: %s", backend)

	colorLogger := charmLog.NewWithOptions(os.Stdout, charmLog.Options{
		Level:           charmLog.InfoLevel,
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
	})
	logger := slog.New(colorLogger)
	metrics := observability.NewMetrics().WithTaskStatsStore(stores.taskStats)
	taskService := application.NewTaskService(
		stores.tasks,
		stores.events,
		stores.taskCreation,
		stores.taskTransition,
	)
	workerTaskService := application.NewWorkerTaskService(
		stores.tasks,
		stores.taskTransition,
	)
	externalRetryScheduler, err := newExternalRetryScheduler(stores.taskTransition)
	if err != nil {
		log.Fatalf("initialize external retry scheduler: %v", err)
	}
	workerTaskService.WithRetryScheduler(externalRetryScheduler).WithMetrics(metrics)
	workerAuthToken := strings.TrimSpace(os.Getenv("TASKPULSE_WORKER_AUTH_TOKEN"))
	if workerAuthToken == "" && os.Getenv("TASKPULSE_INSECURE_ALLOW_UNAUTHENTICATED_WORKERS") != "true" {
		log.Fatal("TASKPULSE_WORKER_AUTH_TOKEN is required; set TASKPULSE_INSECURE_ALLOW_UNAUTHENTICATED_WORKERS=true only for isolated local development")
	}
	router := httptransport.NewRouter(
		httptransport.NewHandlerWithWorker(taskService, workerTaskService).WithWorkerAuthToken(workerAuthToken).WithClaimMetrics(metrics),
		metrics,
	)

	urlCheckExecutor := urlcheck.NewWithConcurrency(
		&http.Client{Timeout: 10 * time.Second},
		5,
	)
	llmClient := llmanalysis.NewFakeClient("fake-llm")
	if err := llmClient.SetFailureMode(os.Getenv("TASKPULSE_LLM_FAKE_FAILURE")); err != nil {
		log.Fatalf("configure fake llm client: %v", err)
	}
	if rawDelay := os.Getenv("TASKPULSE_LLM_FAKE_DELAY"); rawDelay != "" {
		delay, err := time.ParseDuration(rawDelay)
		if err != nil {
			log.Fatalf("parse TASKPULSE_LLM_FAKE_DELAY: %v", err)
		}
		if err := llmClient.SetDelay(delay); err != nil {
			log.Fatalf("configure fake llm delay: %v", err)
		}
	}
	llmAnalysisExecutor, err := llmanalysis.New(llmClient)
	if err != nil {
		log.Fatalf("initialize llm analysis executor: %v", err)
	}
	workerCount := 1
	internalWorkersEnabled := os.Getenv("TASKPULSE_INTERNAL_WORKERS_ENABLED") != "false"
	if !internalWorkersEnabled {
		workerCount = 0
	} else if rawWorkerCount := os.Getenv("TASKPULSE_WORKER_COUNT"); rawWorkerCount != "" {
		workerCount, err = strconv.Atoi(rawWorkerCount)
		if err != nil || workerCount <= 0 {
			log.Fatalf("parse TASKPULSE_WORKER_COUNT: must be a positive integer")
		}
	}
	leaseDuration := 30 * time.Second
	if rawLeaseDuration := os.Getenv("TASKPULSE_WORKER_LEASE_DURATION"); rawLeaseDuration != "" {
		leaseDuration, err = time.ParseDuration(rawLeaseDuration)
		if err != nil {
			log.Fatalf("parse TASKPULSE_WORKER_LEASE_DURATION: %v", err)
		}
		if leaseDuration <= 0 {
			log.Fatalf("configure worker lease duration: %v", err)
		}
	}

	executors := map[string]worker.Executor{
		"url_check":    urlCheckExecutor,
		"llm_analysis": llmAnalysisExecutor,
	}
	retryPolicies := map[string]worker.RetryPolicy{
		"llm_analysis": {
			MaxRetries: 3,
			BaseDelay:  time.Second,
			MaxDelay:   30 * time.Second,
		},
	}
	taskWorkers := make([]*worker.Worker, 0, workerCount)
	for range workerCount {
		taskWorker := worker.New(
			stores.tasks,
			stores.taskTransition,
			executors,
			retryPolicies,
		).WithLogger(logger).WithMetrics(metrics)
		if err := taskWorker.SetLeaseDuration(leaseDuration); err != nil {
			log.Fatalf("configure worker lease duration: %v", err)
		}
		taskWorkers = append(taskWorkers, taskWorker)
	}
	log.Printf("TaskPulse worker count: %d", workerCount)
	taskReaper := worker.NewReaper(stores.taskTransition).WithLogger(logger).WithMetrics(metrics)

	backgroundErrors := make(chan error, workerCount+1)
	for _, taskWorker := range taskWorkers {
		go func(taskWorker *worker.Worker) {
			backgroundErrors <- taskWorker.Run(ctx, 200*time.Millisecond)
		}(taskWorker)
	}
	go func() {
		backgroundErrors <- taskReaper.Run(ctx, time.Second)
	}()

	httpAddr := os.Getenv("TASKPULSE_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = ":8080"
	}
	server := &http.Server{
		Addr:              httpAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	log.Printf("TaskPulse HTTP server listening on %s", server.Addr)
	select {
	case <-ctx.Done():
		log.Print("shutdown signal received")
	case err := <-backgroundErrors:
		if err != nil {
			log.Printf("background processor stopped: %v", err)
		}
		stop()
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server stopped: %v", err)
		}
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown: %v", err)
	}
}

type externalRetryScheduler struct {
	scheduler *worker.RetryScheduler
	policies  map[string]worker.RetryPolicy
}

func newExternalRetryScheduler(transitionStore store.TaskTransitionStore) (*externalRetryScheduler, error) {
	scheduler, err := worker.NewRetryScheduler(
		transitionStore,
		worker.NewDefaultBackoffCalculator(),
	)
	if err != nil {
		return nil, err
	}
	return &externalRetryScheduler{
		scheduler: scheduler,
		policies: map[string]worker.RetryPolicy{
			"llm_analysis": {
				MaxRetries: 3,
				BaseDelay:  time.Second,
				MaxDelay:   30 * time.Second,
			},
		},
	}, nil
}

func (s *externalRetryScheduler) Schedule(
	ctx context.Context,
	task *domain.Task,
	code string,
	message string,
	retryAfter time.Duration,
) error {
	policy, ok := s.policies[task.Workflow]
	if !ok {
		policy = worker.RetryPolicy{
			MaxRetries: task.MaxRetries,
			BaseDelay:  time.Second,
			MaxDelay:   30 * time.Second,
		}
	}
	executionError, err := worker.NewExecutionError(
		worker.ErrorTransient,
		code,
		retryAfter,
		errors.New(message),
	)
	if err != nil {
		return err
	}
	return s.scheduler.Schedule(ctx, task, executionError, policy, time.Now())
}
