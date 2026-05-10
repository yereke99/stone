package handler

import (
	"net/http"
	"time"
)

type HealthHandler struct {
	env       string
	startedAt time.Time
}

func NewHealthHandler(env string) *HealthHandler {
	return &HealthHandler{
		env:       env,
		startedAt: time.Now().UTC(),
	}
}

func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":     "ok",
		"env":        h.env,
		"started_at": h.startedAt.Format(time.RFC3339),
	})
}
