package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zhaozhonghe/taskpulse/internal/domain"
)

func TestMemoryTaskStoreListsTasksWithFiltersAndCursor(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryTaskStore()
	base := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	for index, workflow := range []string{"url_check", "llm_analysis", "llm_analysis"} {
		task, err := domain.NewTask(
			[]string{"task_1", "task_2", "task_3"}[index],
			workflow,
			json.RawMessage(`{}`),
			3,
			base.Add(time.Duration(index)*time.Second),
		)
		if err != nil {
			t.Fatalf("NewTask returned error: %v", err)
		}
		if err := store.Create(ctx, task); err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
	}

	firstPage, err := store.ListTasks(ctx, ListTasksOptions{Workflow: "llm_analysis", Limit: 1})
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(firstPage) != 1 || firstPage[0].ID != "task_3" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}

	cursorTime := firstPage[0].CreatedAt
	secondPage, err := store.ListTasks(ctx, ListTasksOptions{
		Workflow:        "llm_analysis",
		BeforeCreatedAt: &cursorTime,
		BeforeID:        firstPage[0].ID,
		Limit:           2,
	})
	if err != nil {
		t.Fatalf("ListTasks second page returned error: %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID != "task_2" {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}
}
