package urlcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/worker"
)

func TestExecutorReturnsPartialResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ok", http.StatusFound)
	})
	mux.HandleFunc("/failed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	executor := New(server.Client())
	task := newURLCheckTask(t, []string{server.URL + "/ok", server.URL + "/redirect", server.URL + "/failed"})
	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Outcome != worker.OutcomePartial {
		t.Fatalf("expected partial outcome, got %s", result.Outcome)
	}

	var output Output
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.Succeeded != 2 || output.Failed != 1 {
		t.Fatalf("unexpected output summary: %+v", output)
	}
	if output.Items[1].FinalURL != server.URL+"/ok" {
		t.Fatalf("expected redirect final URL, got %s", output.Items[1].FinalURL)
	}
}

func TestExecutorRejectsEmptyURLList(t *testing.T) {
	executor := New(nil)
	task := newURLCheckTask(t, nil)
	if _, err := executor.Execute(context.Background(), task); err == nil {
		t.Fatal("expected empty URL list to be rejected")
	}
}

func TestExecutorReturnsFailedWhenAllItemsFail(t *testing.T) {
	executor := New(nil)
	task := newURLCheckTask(t, []string{"not-a-url", "ftp://example.com/file"})
	result, err := executor.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Outcome != worker.OutcomeFailed {
		t.Fatalf("expected failed outcome, got %s", result.Outcome)
	}
}

func TestExecutorLimitsConcurrencyAndPreservesOrder(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	urls := []string{
		server.URL + "/first",
		server.URL + "/second",
		server.URL + "/third",
		server.URL + "/fourth",
	}
	executor := NewWithConcurrency(server.Client(), 2)
	result, err := executor.Execute(context.Background(), newURLCheckTask(t, urls))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("expected maximum concurrency 2, got %d", maximum.Load())
	}

	var output Output
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	for index, item := range output.Items {
		if item.URL != urls[index] {
			t.Fatalf("result %d has URL %s, expected %s", index, item.URL, urls[index])
		}
	}
}

func newURLCheckTask(t *testing.T, urls []string) *domain.Task {
	t.Helper()
	input, err := json.Marshal(Input{URLs: urls})
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	task, err := domain.NewTask("task_1", "url_check", input, 0, time.Now())
	if err != nil {
		t.Fatalf("NewTask returned error: %v", err)
	}
	return task
}
