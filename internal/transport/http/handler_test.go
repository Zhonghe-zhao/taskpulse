package httptransport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhonghe-zhao/taskpulse/internal/application"
	"github.com/Zhonghe-zhao/taskpulse/internal/domain"
	"github.com/Zhonghe-zhao/taskpulse/internal/store"
)

func TestCreateGetAndListTaskEvents(t *testing.T) {
	router := newTestRouter()
	body := `{"workflow":"url_check","input":{"urls":["https://example.com"]},"max_retries":3}`
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body)))
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}

	var created domain.Task
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}
	if created.ID == "" || created.Status != domain.TaskStatusQueued {
		t.Fatalf("unexpected created task: %+v", created)
	}

	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID, nil))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, getResponse.Code, getResponse.Body.String())
	}

	eventsResponse := httptest.NewRecorder()
	router.ServeHTTP(eventsResponse, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil))
	if eventsResponse.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, eventsResponse.Code, eventsResponse.Body.String())
	}

	var events []*domain.TaskEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode task events: %v", err)
	}
	if len(events) != 1 || events[0].Type != domain.EventTaskCreated {
		t.Fatalf("unexpected task events: %+v", events)
	}
}

func TestDashboardReturnsEmbeddedHTML(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected HTML content type, got %q", contentType)
	}
	if !strings.Contains(response.Body.String(), "TaskPulse Control") {
		t.Fatal("expected embedded dashboard content")
	}
}

func TestDashboardReturnsEmbeddedJavaScript(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "text/javascript") {
		t.Fatalf("expected JavaScript content type, got %q", contentType)
	}
	if !strings.Contains(response.Body.String(), "refreshAll") {
		t.Fatal("expected embedded dashboard JavaScript")
	}
}

func TestDashboardStatusFilterDoesNotNarrowOverviewStats(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/dashboard.js", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	script := response.Body.String()
	start := strings.Index(script, "async function refreshStats()")
	end := strings.Index(script, "async function refreshRuntimeMetrics()")
	if start == -1 || end == -1 || end <= start {
		t.Fatal("expected refreshStats function in dashboard JavaScript")
	}
	refreshStats := script[start:end]
	if strings.Contains(refreshStats, "params.set('status'") {
		t.Fatal("overview stats must not be narrowed by the task-list status filter")
	}
	if !strings.Contains(refreshStats, "params.set('workflow'") {
		t.Fatal("overview stats must preserve the workflow scope")
	}
}

func TestGetTaskDoesNotExposeLeaseToken(t *testing.T) {
	router := newWorkerTestRouter()
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		strings.NewReader(`{"workflow":"llm_analysis","input":{}}`),
	))
	var created domain.Task
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	claimResponse := httptest.NewRecorder()
	claimBody := `{"worker_id":"worker-1","workflow":"llm_analysis","lease_duration":"30s"}`
	router.ServeHTTP(claimResponse, httptest.NewRequest(http.MethodPost, "/worker/tasks/claim", strings.NewReader(claimBody)))
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("claim task returned %d: %s", claimResponse.Code, claimResponse.Body.String())
	}
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID, nil))
	if strings.Contains(getResponse.Body.String(), "lease_token") {
		t.Fatalf("task detail leaked lease token: %s", getResponse.Body.String())
	}
}

func TestListTasksSupportsFilteringAndPagination(t *testing.T) {
	router := newTestRouter()
	for _, workflow := range []string{"url_check", "llm_analysis", "llm_analysis"} {
		body := fmt.Sprintf(`{"workflow":%q,"input":{},"max_retries":3}`, workflow)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body)))
		if response.Code != http.StatusCreated {
			t.Fatalf("create task returned %d: %s", response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks?workflow=llm_analysis&status=queued&limit=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list tasks returned %d: %s", response.Code, response.Body.String())
	}
	var first application.ListTasksResult
	if err := json.NewDecoder(response.Body).Decode(&first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Items) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}

	secondResponse := httptest.NewRecorder()
	secondPath := "/tasks?workflow=llm_analysis&status=queued&limit=1&cursor=" + first.NextCursor
	router.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, secondPath, nil))
	if secondResponse.Code != http.StatusOK {
		t.Fatalf("list second page returned %d: %s", secondResponse.Code, secondResponse.Body.String())
	}
}

func TestTaskStatsUsesSameWorkflowAndStatusScopeAsTaskList(t *testing.T) {
	router := newTestRouter()
	for _, workflow := range []string{"llm_analysis", "memobridge.semantic_profile", "memobridge.semantic_profile"} {
		body := fmt.Sprintf(`{"workflow":%q,"input":{}}`, workflow)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body)))
		if response.Code != http.StatusCreated {
			t.Fatalf("create task returned %d: %s", response.Code, response.Body.String())
		}
	}

	statsResponse := httptest.NewRecorder()
	path := "/task-stats?workflow=memobridge.semantic_profile&status=queued"
	router.ServeHTTP(statsResponse, httptest.NewRequest(http.MethodGet, path, nil))
	if statsResponse.Code != http.StatusOK {
		t.Fatalf("task stats returned %d: %s", statsResponse.Code, statsResponse.Body.String())
	}
	var stats application.TaskStatsResult
	if err := json.NewDecoder(statsResponse.Body).Decode(&stats); err != nil {
		t.Fatalf("decode task stats: %v", err)
	}
	if stats.StatusCounts[domain.TaskStatusQueued] != 2 {
		t.Fatalf("unexpected filtered status counts: %+v", stats.StatusCounts)
	}
}

func TestCreateTaskRejectsInvalidJSON(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"workflow":`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, response.Code)
	}
}

func TestCreateTaskSupportsIdempotentReplayAndConflict(t *testing.T) {
	router := newTestRouter()
	firstRequest := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		strings.NewReader(`{"workflow":"llm_analysis","input":{"subject":"go"},"max_retries":3}`),
	)
	firstRequest.Header.Set("Idempotency-Key", "memobridge-analysis-1")
	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("expected first status %d, got %d: %s", http.StatusCreated, firstResponse.Code, firstResponse.Body.String())
	}
	var first domain.Task
	if err := json.NewDecoder(firstResponse.Body).Decode(&first); err != nil {
		t.Fatalf("decode first task: %v", err)
	}

	replayRequest := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		strings.NewReader(`{"workflow":"llm_analysis","input":{"subject":"go"},"max_retries":3}`),
	)
	replayRequest.Header.Set("Idempotency-Key", "memobridge-analysis-1")
	replayResponse := httptest.NewRecorder()
	router.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("expected replay status %d, got %d: %s", http.StatusOK, replayResponse.Code, replayResponse.Body.String())
	}
	var replayed domain.Task
	if err := json.NewDecoder(replayResponse.Body).Decode(&replayed); err != nil {
		t.Fatalf("decode replayed task: %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("expected replayed task %s, got %s", first.ID, replayed.ID)
	}

	conflictRequest := httptest.NewRequest(
		http.MethodPost,
		"/tasks",
		strings.NewReader(`{"workflow":"llm_analysis","input":{"subject":"database"},"max_retries":3}`),
	)
	conflictRequest.Header.Set("Idempotency-Key", "memobridge-analysis-1")
	conflictResponse := httptest.NewRecorder()
	router.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("expected conflict status %d, got %d: %s", http.StatusConflict, conflictResponse.Code, conflictResponse.Body.String())
	}
}

func TestGetTaskReturnsNotFound(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, response.Code)
	}
}

func TestCancelTaskIsIdempotent(t *testing.T) {
	router := newTestRouter()
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(
		createResponse,
		httptest.NewRequest(
			http.MethodPost,
			"/tasks",
			strings.NewReader(`{"workflow":"llm_analysis","input":{"subject":"go"}}`),
		),
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}
	var created domain.Task
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		router.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "/tasks/"+created.ID+"/cancel", nil),
		)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected status %d, got %d: %s", attempt, http.StatusOK, response.Code, response.Body.String())
		}
		var canceled domain.Task
		if err := json.NewDecoder(response.Body).Decode(&canceled); err != nil {
			t.Fatalf("attempt %d: decode canceled task: %v", attempt, err)
		}
		if canceled.Status != domain.TaskStatusCanceled {
			t.Fatalf("attempt %d: unexpected canceled task: %+v", attempt, canceled)
		}
	}

	eventsResponse := httptest.NewRecorder()
	router.ServeHTTP(
		eventsResponse,
		httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil),
	)
	var events []*domain.TaskEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != 2 || events[1].Type != domain.EventTaskCanceled {
		t.Fatalf("unexpected events after repeated cancellation: %+v", events)
	}
}

func TestCancelTaskReturnsNotFound(t *testing.T) {
	router := newTestRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/tasks/missing/cancel", nil),
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, response.Code, response.Body.String())
	}
}

func TestWorkerCompleteRejectsMissingLeaseToken(t *testing.T) {
	router := newWorkerTestRouter()
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(
		createResponse,
		httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"workflow":"llm_analysis","input":{}}`)),
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}
	var created domain.Task
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	claimResponse := httptest.NewRecorder()
	router.ServeHTTP(
		claimResponse,
		httptest.NewRequest(http.MethodPost, "/worker/tasks/claim", strings.NewReader(`{"worker_id":"worker-1","workflow":"llm_analysis","lease_duration":"30s"}`)),
	)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("expected claim status %d, got %d: %s", http.StatusOK, claimResponse.Code, claimResponse.Body.String())
	}

	completeResponse := httptest.NewRecorder()
	router.ServeHTTP(
		completeResponse,
		httptest.NewRequest(http.MethodPost, "/worker/tasks/"+created.ID+"/complete", strings.NewReader(`{"worker_id":"worker-1","result_ref":{"source_item_id":1}}`)),
	)
	if completeResponse.Code != http.StatusConflict {
		t.Fatalf("expected missing lease token status %d, got %d: %s", http.StatusConflict, completeResponse.Code, completeResponse.Body.String())
	}
}

func TestWorkerReleaseIsIdempotentAndRequeuesTask(t *testing.T) {
	router := newWorkerTestRouter()
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(
		createResponse,
		httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"workflow":"llm_analysis","input":{},"max_retries":3}`)),
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d: %s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}
	var created domain.Task
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created task: %v", err)
	}

	claimResponse := httptest.NewRecorder()
	router.ServeHTTP(
		claimResponse,
		httptest.NewRequest(http.MethodPost, "/worker/tasks/claim", strings.NewReader(`{"worker_id":"worker-1","workflow":"llm_analysis","lease_duration":"30s"}`)),
	)
	if claimResponse.Code != http.StatusOK {
		t.Fatalf("expected claim status %d, got %d: %s", http.StatusOK, claimResponse.Code, claimResponse.Body.String())
	}
	var claimed domain.Task
	if err := json.NewDecoder(claimResponse.Body).Decode(&claimed); err != nil {
		t.Fatalf("decode claimed task: %v", err)
	}

	body := `{"worker_id":"worker-1","lease_token":"` + claimed.LeaseToken + `","version":` + fmt.Sprint(claimed.Version) + `}`
	for attempt := 0; attempt < 2; attempt++ {
		releaseResponse := httptest.NewRecorder()
		router.ServeHTTP(
			releaseResponse,
			httptest.NewRequest(http.MethodPost, "/worker/tasks/"+created.ID+"/release", strings.NewReader(body)),
		)
		if releaseResponse.Code != http.StatusOK {
			t.Fatalf("attempt %d: expected release status %d, got %d: %s", attempt, http.StatusOK, releaseResponse.Code, releaseResponse.Body.String())
		}
		var released domain.Task
		if err := json.NewDecoder(releaseResponse.Body).Decode(&released); err != nil {
			t.Fatalf("attempt %d: decode released task: %v", attempt, err)
		}
		if released.Status != domain.TaskStatusQueued || released.RetryCount != 0 || released.LeaseOwner != "" {
			t.Fatalf("attempt %d: unexpected released task: %+v", attempt, released)
		}
	}

	eventsResponse := httptest.NewRecorder()
	router.ServeHTTP(eventsResponse, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil))
	var events []*domain.TaskEvent
	if err := json.NewDecoder(eventsResponse.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	releasedEvents := 0
	for _, event := range events {
		if event.Type == domain.EventTaskReleased {
			releasedEvents++
		}
	}
	if releasedEvents != 1 {
		t.Fatalf("expected exactly one release event, got %d: %+v", releasedEvents, events)
	}
}

func TestWorkerEndpointsRequireBearerTokenWhenConfigured(t *testing.T) {
	router := newWorkerTestRouterWithAuthToken("worker-secret")
	body := `{"worker_id":"worker-1","workflow":"llm_analysis","lease_duration":"30s"}`

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/worker/tasks/claim", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized worker request, got %d: %s", unauthorized.Code, unauthorized.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, "/worker/tasks/claim", strings.NewReader(body))
	authorizedRequest.Header.Set("Authorization", "Bearer worker-secret")
	authorized := httptest.NewRecorder()
	router.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("expected authorized empty claim, got %d: %s", authorized.Code, authorized.Body.String())
	}
}

func newTestRouter() http.Handler {
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	taskCreationStore := store.NewMemoryTaskCreationStore(taskStore, eventStore)
	taskTransitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	service := application.NewTaskService(
		taskStore,
		eventStore,
		taskCreationStore,
		taskTransitionStore,
	)
	return NewRouter(NewHandler(service))
}

func newWorkerTestRouter() http.Handler {
	return newWorkerTestRouterWithAuthToken("")
}

func newWorkerTestRouterWithAuthToken(workerAuthToken string) http.Handler {
	taskStore := store.NewMemoryTaskStore()
	eventStore := store.NewMemoryEventStore()
	taskCreationStore := store.NewMemoryTaskCreationStore(taskStore, eventStore)
	taskTransitionStore := store.NewMemoryTaskTransitionStore(taskStore, eventStore)
	taskService := application.NewTaskService(
		taskStore,
		eventStore,
		taskCreationStore,
		taskTransitionStore,
	)
	workerService := application.NewWorkerTaskService(taskStore, taskTransitionStore)
	return NewRouter(NewHandlerWithWorker(taskService, workerService).WithWorkerAuthToken(workerAuthToken))
}
