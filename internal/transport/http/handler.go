package httptransport

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/application"
	"github.com/zhaozhonghe/taskpulse/internal/domain"
	"github.com/zhaozhonghe/taskpulse/internal/store"
)

const maxRequestBodyBytes = 1 << 20

type Handler struct {
	taskService       *application.TaskService
	workerTaskService *application.WorkerTaskService
	claimMetrics      claimMetricsRecorder
	workerAuthToken   string
}

type claimMetricsRecorder interface {
	RecordClaimAttempt(workflow string)
	RecordClaimMiss(workflow string)
}

func NewHandler(taskService *application.TaskService) *Handler {
	return &Handler{taskService: taskService}
}

func NewHandlerWithWorker(
	taskService *application.TaskService,
	workerTaskService *application.WorkerTaskService,
) *Handler {
	return &Handler{
		taskService:       taskService,
		workerTaskService: workerTaskService,
	}
}

func (h *Handler) WithClaimMetrics(metrics claimMetricsRecorder) *Handler {
	h.claimMetrics = metrics
	return h
}

// WithWorkerAuthToken requires a bearer token for every Worker protocol call.
// An empty token is only intended for isolated unit tests or explicitly
// configured insecure local development.
func (h *Handler) WithWorkerAuthToken(token string) *Handler {
	h.workerAuthToken = strings.TrimSpace(token)
	return h
}

func (h *Handler) authorizeWorker(w http.ResponseWriter, r *http.Request) bool {
	if h.workerAuthToken == "" {
		return true
	}
	const prefix = "Bearer "
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, prefix) {
		writeError(w, http.StatusUnauthorized, "worker authentication is required")
		return false
	}
	provided := strings.TrimPrefix(authorization, prefix)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.workerAuthToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "worker authentication is required")
		return false
	}
	return true
}

type createTaskRequest struct {
	Workflow   string          `json:"workflow"`
	Input      json.RawMessage `json:"input"`
	MaxRetries int             `json:"max_retries"`
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var request createTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.taskService.CreateTask(r.Context(), application.CreateTaskInput{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		Workflow:       request.Workflow,
		Input:          request.Input,
		MaxRetries:     request.MaxRetries,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result.Task)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	task, err := h.taskService.GetTaskDetail(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	result, err := h.taskService.ListTasks(r.Context(), application.ListTasksInput{
		Workflow: r.URL.Query().Get("workflow"),
		Status:   domain.TaskStatus(r.URL.Query().Get("status")),
		Cursor:   r.URL.Query().Get("cursor"),
		Limit:    limit,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetTaskStats(w http.ResponseWriter, r *http.Request) {
	result, err := h.taskService.GetTaskStats(r.Context(), application.TaskStatsInput{
		Workflow: r.URL.Query().Get("workflow"),
		Status:   domain.TaskStatus(r.URL.Query().Get("status")),
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	_, err := h.taskService.CancelTask(r.Context(), taskID)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	detail, err := h.taskService.GetTaskDetail(r.Context(), taskID)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) ListTaskEvents(w http.ResponseWriter, r *http.Request) {
	events, err := h.taskService.ListTaskEvents(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

type workerClaimRequest struct {
	WorkerID      string `json:"worker_id"`
	Workflow      string `json:"workflow"`
	LeaseDuration string `json:"lease_duration"`
}

type workerHeartbeatRequest struct {
	WorkerID      string `json:"worker_id"`
	LeaseToken    string `json:"lease_token"`
	LeaseDuration string `json:"lease_duration"`
}

type workerCompleteRequest struct {
	WorkerID   string          `json:"worker_id"`
	LeaseToken string          `json:"lease_token"`
	Version    uint64          `json:"version"`
	Output     json.RawMessage `json:"output"`
	ResultRef  json.RawMessage `json:"result_ref"`
}

type workerProgressRequest struct {
	WorkerID   string `json:"worker_id"`
	LeaseToken string `json:"lease_token"`
	Version    uint64 `json:"version"`
	Progress   int    `json:"progress"`
	Message    string `json:"message"`
}

type workerFailRequest struct {
	WorkerID     string `json:"worker_id"`
	LeaseToken   string `json:"lease_token"`
	Version      uint64 `json:"version"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Retryable    bool   `json:"retryable"`
	RetryAfter   string `json:"retry_after"`
}

type workerReleaseRequest struct {
	WorkerID   string `json:"worker_id"`
	LeaseToken string `json:"lease_token"`
	Version    uint64 `json:"version"`
}

func (h *Handler) ClaimWorkerTask(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerClaimRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	leaseDuration, err := parseLeaseDuration(request.LeaseDuration)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if h.claimMetrics != nil {
		h.claimMetrics.RecordClaimAttempt(request.Workflow)
	}
	task, err := h.workerTaskService.ClaimTask(r.Context(), application.ClaimTaskInput{
		WorkerID:      request.WorkerID,
		Workflow:      request.Workflow,
		LeaseDuration: leaseDuration,
	})
	if errors.Is(err, store.ErrNoTaskAvailable) {
		if h.claimMetrics != nil {
			h.claimMetrics.RecordClaimMiss(request.Workflow)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) HeartbeatWorkerTask(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerHeartbeatRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	leaseDuration, err := parseLeaseDuration(request.LeaseDuration)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	task, err := h.workerTaskService.HeartbeatTask(r.Context(), application.HeartbeatTaskInput{
		TaskID:        r.PathValue("task_id"),
		WorkerID:      request.WorkerID,
		LeaseToken:    request.LeaseToken,
		LeaseDuration: leaseDuration,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) CompleteWorkerTask(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerCompleteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, err := h.workerTaskService.CompleteTask(r.Context(), application.CompleteTaskInput{
		TaskID:     r.PathValue("task_id"),
		WorkerID:   request.WorkerID,
		LeaseToken: request.LeaseToken,
		Version:    request.Version,
		Output:     request.Output,
		ResultRef:  request.ResultRef,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) ReportWorkerProgress(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerProgressRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, err := h.workerTaskService.ReportProgress(r.Context(), application.ReportProgressInput{
		TaskID:     r.PathValue("task_id"),
		WorkerID:   request.WorkerID,
		LeaseToken: request.LeaseToken,
		Version:    request.Version,
		Progress:   request.Progress,
		Message:    request.Message,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) FailWorkerTask(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerFailRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	retryAfter, err := parseOptionalDuration(request.RetryAfter)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	task, err := h.workerTaskService.FailTask(r.Context(), application.FailTaskInput{
		TaskID:       r.PathValue("task_id"),
		WorkerID:     request.WorkerID,
		LeaseToken:   request.LeaseToken,
		Version:      request.Version,
		ErrorCode:    request.ErrorCode,
		ErrorMessage: request.ErrorMessage,
		Retryable:    request.Retryable,
		RetryAfter:   retryAfter,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) ReleaseWorkerTask(w http.ResponseWriter, r *http.Request) {
	if h.workerTaskService == nil {
		writeError(w, http.StatusNotFound, "worker protocol is not configured")
		return
	}
	if !h.authorizeWorker(w, r) {
		return
	}
	var request workerReleaseRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task, err := h.workerTaskService.ReleaseTask(r.Context(), application.ReleaseTaskInput{
		TaskID:     r.PathValue("task_id"),
		WorkerID:   request.WorkerID,
		LeaseToken: request.LeaseToken,
		Version:    request.Version,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func parseOptionalDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("%w: retry_after must be a non-negative duration", application.ErrInvalidWorkerRequest)
	}
	return duration, nil
}

func parseLeaseDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("%w: lease_duration is required", application.ErrInvalidWorkerRequest)
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%w: lease_duration must be a positive duration", application.ErrInvalidWorkerRequest)
	}
	return duration, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

func writeApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, application.ErrInvalidWorkerRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrTaskNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrTaskAlreadyExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrTaskNotCancelable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrLeaseLost),
		errors.Is(err, store.ErrTaskConflict):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}
