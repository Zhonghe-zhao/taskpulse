package httptransport

import (
	_ "embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardHTML []byte

//go:embed dashboard.js
var dashboardJS []byte

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(dashboardHTML)
}

func (h *Handler) DashboardScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(dashboardJS)
}
