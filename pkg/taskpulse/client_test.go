package taskpulse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCreateTaskUsesProtocolFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "idem-1" {
			t.Fatalf("unexpected idempotency key %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var request struct {
			Workflow   string          `json:"workflow"`
			Input      json.RawMessage `json:"input"`
			MaxRetries int             `json:"max_retries"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if request.Workflow != "memobridge.semantic_profile" || request.MaxRetries != 3 {
			t.Fatalf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","workflow":"memobridge.semantic_profile","status":"queued"}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL}
	task, err := client.CreateTask(context.Background(), CreateTaskRequest{
		Workflow:       "memobridge.semantic_profile",
		Input:          json.RawMessage(`{"source_item_id":11778}`),
		MaxRetries:     3,
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.ID != "task-1" || task.Status != "queued" {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestClientClaimReturnsNoTaskAvailableFor204(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer worker-secret" {
			t.Fatalf("unexpected worker authorization %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, WorkerAuthToken: "worker-secret"}
	_, err := client.Claim(context.Background(), ClaimRequest{
		WorkerID:      "worker-1",
		Workflow:      "memobridge.semantic_profile",
		LeaseDuration: 30 * time.Second,
	})
	if !errors.Is(err, ErrNoTaskAvailable) {
		t.Fatalf("expected ErrNoTaskAvailable, got %v", err)
	}
}

func TestClientReleaseUsesWorkerAuthAndFencingFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/worker/tasks/task-1/release" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer worker-secret" {
			t.Fatalf("unexpected worker authorization %q", got)
		}
		var request struct {
			WorkerID   string `json:"worker_id"`
			LeaseToken string `json:"lease_token"`
			Version    uint64 `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode release request: %v", err)
		}
		if request.WorkerID != "worker-1" || request.LeaseToken != "lease-token" || request.Version != 7 {
			t.Fatalf("unexpected release request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task-1","workflow":"llm_analysis","status":"queued","version":8}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, WorkerAuthToken: "worker-secret"}
	task, err := client.Release(context.Background(), ReleaseRequest{
		TaskID: "task-1", WorkerID: "worker-1", LeaseToken: "lease-token", Version: 7,
	})
	if err != nil {
		t.Fatalf("Release returned error: %v", err)
	}
	if task.Status != "queued" || task.Version != 8 {
		t.Fatalf("unexpected released task: %+v", task)
	}
}

func TestHTTPErrorIncludesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("idempotency conflict"))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL}
	_, err := client.GetTask(context.Background(), "task-1")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected HTTPError, got %v", err)
	}
	if httpErr.StatusCode != http.StatusConflict || !strings.Contains(httpErr.Body, "idempotency conflict") {
		t.Fatalf("unexpected HTTPError: %+v", httpErr)
	}
}
