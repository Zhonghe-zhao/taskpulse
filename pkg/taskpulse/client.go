package taskpulse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var ErrNoTaskAvailable = errors.New("no task available")

type Client struct {
	BaseURL         string
	HTTPClient      *http.Client
	WorkerAuthToken string
}

type Task struct {
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Workflow       string          `json:"workflow"`
	Status         string          `json:"status"`
	Input          json.RawMessage `json:"input"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	Progress       int             `json:"progress"`
	RetryCount     int             `json:"retry_count"`
	MaxRetries     int             `json:"max_retries"`
	Version        uint64          `json:"version"`
	LeaseToken     string          `json:"lease_token,omitempty"`
	LeaseUntil     *time.Time      `json:"lease_until,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

type TaskEvent struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Progress  int             `json:"progress"`
	CreatedAt time.Time       `json:"created_at"`
}

type CreateTaskRequest struct {
	Workflow       string
	Input          json.RawMessage
	MaxRetries     int
	IdempotencyKey string
}

type ClaimRequest struct {
	WorkerID      string
	Workflow      string
	LeaseDuration time.Duration
}

type CompleteRequest struct {
	TaskID     string
	WorkerID   string
	LeaseToken string
	Version    uint64
	Output     json.RawMessage
	ResultRef  json.RawMessage
}

type FailRequest struct {
	TaskID       string
	WorkerID     string
	LeaseToken   string
	Version      uint64
	ErrorCode    string
	ErrorMessage string
	Retryable    bool
	RetryAfter   time.Duration
}

type ReleaseRequest struct {
	TaskID     string
	WorkerID   string
	LeaseToken string
	Version    uint64
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("taskpulse HTTP %d: %s", e.StatusCode, e.Body)
}

func (c *Client) CreateTask(ctx context.Context, request CreateTaskRequest) (*Task, error) {
	body := struct {
		Workflow   string          `json:"workflow"`
		Input      json.RawMessage `json:"input"`
		MaxRetries int             `json:"max_retries"`
	}{
		Workflow:   request.Workflow,
		Input:      request.Input,
		MaxRetries: request.MaxRetries,
	}
	if len(body.Input) == 0 {
		body.Input = json.RawMessage(`{}`)
	}
	var task Task
	headers := http.Header{}
	if request.IdempotencyKey != "" {
		headers.Set("Idempotency-Key", request.IdempotencyKey)
	}
	if err := c.doJSON(ctx, http.MethodPost, "/tasks", body, headers, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	var task Task
	if err := c.doJSON(ctx, http.MethodGet, "/tasks/"+url.PathEscape(taskID), nil, nil, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *Client) ListTaskEvents(ctx context.Context, taskID string) ([]TaskEvent, error) {
	var events []TaskEvent
	if err := c.doJSON(ctx, http.MethodGet, "/tasks/"+url.PathEscape(taskID)+"/events", nil, nil, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	var task Task
	if err := c.doJSON(ctx, http.MethodPost, "/tasks/"+url.PathEscape(taskID)+"/cancel", nil, nil, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *Client) Claim(ctx context.Context, request ClaimRequest) (*Task, error) {
	body := struct {
		WorkerID      string `json:"worker_id"`
		Workflow      string `json:"workflow,omitempty"`
		LeaseDuration string `json:"lease_duration"`
	}{
		WorkerID:      request.WorkerID,
		Workflow:      request.Workflow,
		LeaseDuration: request.LeaseDuration.String(),
	}
	var task Task
	if err := c.doJSON(ctx, http.MethodPost, "/worker/tasks/claim", body, c.workerHeaders(), &task); err != nil {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNoContent {
			return nil, ErrNoTaskAvailable
		}
		return nil, err
	}
	return &task, nil
}

func (c *Client) Heartbeat(ctx context.Context, taskID, workerID, leaseToken string, leaseDuration time.Duration) (*Task, error) {
	body := struct {
		WorkerID      string `json:"worker_id"`
		LeaseToken    string `json:"lease_token"`
		LeaseDuration string `json:"lease_duration"`
	}{workerID, leaseToken, leaseDuration.String()}
	return c.workerTask(ctx, http.MethodPost, "/worker/tasks/"+url.PathEscape(taskID)+"/heartbeat", body)
}

func (c *Client) Progress(ctx context.Context, taskID, workerID, leaseToken string, version uint64, progress int, message string) (*Task, error) {
	body := struct {
		WorkerID   string `json:"worker_id"`
		LeaseToken string `json:"lease_token"`
		Version    uint64 `json:"version"`
		Progress   int    `json:"progress"`
		Message    string `json:"message"`
	}{workerID, leaseToken, version, progress, message}
	return c.workerTask(ctx, http.MethodPost, "/worker/tasks/"+url.PathEscape(taskID)+"/progress", body)
}

func (c *Client) Complete(ctx context.Context, request CompleteRequest) (*Task, error) {
	body := struct {
		WorkerID   string          `json:"worker_id"`
		LeaseToken string          `json:"lease_token"`
		Version    uint64          `json:"version,omitempty"`
		Output     json.RawMessage `json:"output,omitempty"`
		ResultRef  json.RawMessage `json:"result_ref,omitempty"`
	}{request.WorkerID, request.LeaseToken, request.Version, request.Output, request.ResultRef}
	return c.workerTask(ctx, http.MethodPost, "/worker/tasks/"+url.PathEscape(request.TaskID)+"/complete", body)
}

func (c *Client) Fail(ctx context.Context, request FailRequest) (*Task, error) {
	body := struct {
		WorkerID     string `json:"worker_id"`
		LeaseToken   string `json:"lease_token"`
		Version      uint64 `json:"version,omitempty"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Retryable    bool   `json:"retryable"`
		RetryAfter   string `json:"retry_after,omitempty"`
	}{
		WorkerID: request.WorkerID, LeaseToken: request.LeaseToken, Version: request.Version,
		ErrorCode: request.ErrorCode, ErrorMessage: request.ErrorMessage,
		Retryable: request.Retryable, RetryAfter: formatOptionalDuration(request.RetryAfter),
	}
	return c.workerTask(ctx, http.MethodPost, "/worker/tasks/"+url.PathEscape(request.TaskID)+"/fail", body)
}

func (c *Client) Release(ctx context.Context, request ReleaseRequest) (*Task, error) {
	body := struct {
		WorkerID   string `json:"worker_id"`
		LeaseToken string `json:"lease_token"`
		Version    uint64 `json:"version,omitempty"`
	}{request.WorkerID, request.LeaseToken, request.Version}
	return c.workerTask(ctx, http.MethodPost, "/worker/tasks/"+url.PathEscape(request.TaskID)+"/release", body)
}

func (c *Client) workerTask(ctx context.Context, method, path string, body any) (*Task, error) {
	var task Task
	if err := c.doJSON(ctx, method, path, body, c.workerHeaders(), &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (c *Client) workerHeaders() http.Header {
	token := strings.TrimSpace(c.WorkerAuthToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TASKPULSE_WORKER_AUTH_TOKEN"))
	}
	if token == "" {
		return nil
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	return headers
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, headers http.Header, result any) error {
	baseURL := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if baseURL == "" {
		return errors.New("taskpulse base URL is required")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode taskpulse request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create taskpulse request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("call taskpulse: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if readErr != nil {
			return fmt.Errorf("read taskpulse error response: %w", readErr)
		}
		return &HTTPError{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if response.StatusCode == http.StatusNoContent {
		return &HTTPError{StatusCode: response.StatusCode}
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode taskpulse response: %w", err)
	}
	return nil
}

func formatOptionalDuration(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}
