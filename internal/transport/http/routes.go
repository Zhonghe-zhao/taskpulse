package httptransport

import "net/http"

func NewRouter(handler *Handler, metricsHandler ...http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", handler.Dashboard)
	mux.HandleFunc("GET /dashboard", handler.Dashboard)
	mux.HandleFunc("GET /dashboard.js", handler.DashboardScript)
	mux.HandleFunc("POST /tasks", handler.CreateTask)
	mux.HandleFunc("GET /tasks", handler.ListTasks)
	mux.HandleFunc("GET /task-stats", handler.GetTaskStats)
	mux.HandleFunc("GET /tasks/{task_id}", handler.GetTask)
	mux.HandleFunc("POST /tasks/{task_id}/cancel", handler.CancelTask)
	mux.HandleFunc("GET /tasks/{task_id}/events", handler.ListTaskEvents)
	if handler.workerTaskService != nil {
		mux.HandleFunc("POST /worker/tasks/claim", handler.ClaimWorkerTask)
		mux.HandleFunc("POST /worker/tasks/{task_id}/heartbeat", handler.HeartbeatWorkerTask)
		mux.HandleFunc("POST /worker/tasks/{task_id}/progress", handler.ReportWorkerProgress)
		mux.HandleFunc("POST /worker/tasks/{task_id}/complete", handler.CompleteWorkerTask)
		mux.HandleFunc("POST /worker/tasks/{task_id}/fail", handler.FailWorkerTask)
		mux.HandleFunc("POST /worker/tasks/{task_id}/release", handler.ReleaseWorkerTask)
	}
	if len(metricsHandler) > 0 && metricsHandler[0] != nil {
		mux.Handle("GET /metrics", metricsHandler[0])
	}
	return mux
}
