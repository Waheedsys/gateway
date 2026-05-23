package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/repository"
	"github.com/go-chi/chi/v5"
)

type IngestionHandler struct {
	inferenceLogs *repository.InferenceLog
}

func NewIngestionHandler(inferenceLogs *repository.InferenceLog) *IngestionHandler {
	return &IngestionHandler{inferenceLogs: inferenceLogs}
}

func (h *IngestionHandler) Routes(r chi.Router) {
	r.Post("/ingest/inference-logs", h.CreateInferenceLog)
	r.Get("/inference/stats", h.GetInferenceStats)
}

func (h *IngestionHandler) CreateInferenceLog(w http.ResponseWriter, r *http.Request) {
	var req models.InferenceLog
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.Status) == "" {
		writeError(w, http.StatusBadRequest, "provider, model, and status are required")
		return
	}
	if req.Status != "success" && req.Status != "error" {
		writeError(w, http.StatusBadRequest, "status must be success or error")
		return
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now().UTC()
	}
	if req.RawMetadata == nil {
		req.RawMetadata = map[string]any{}
	}
	req.InputPreview = preview(req.InputPreview, 200)
	req.OutputPreview = preview(req.OutputPreview, 200)

	created, err := h.inferenceLogs.Create(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store inference log")
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (h *IngestionHandler) GetInferenceStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.inferenceLogs.GetStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load inference stats")
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
