package proxy

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/Waheedsys/ai-gateway/internal/providers"
	"github.com/Waheedsys/ai-gateway/pkg/models"
)

type Handler struct {
	provider providers.Provider // interface — not a concrete type
}

func New(p providers.Provider) *Handler {
	return &Handler{provider: p}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 1. Decode the incoming request body
	var req models.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Default model if none provided
	if req.Model == "" {
		req.Model = "claude-sonnet-4-5"
	}

	// 2. Call the provider — this opens a connection, doesn't buffer
	upstream, err := h.provider.Complete(r.Context(), req.Model, req.Prompt, req.Stream)
	if err != nil {
		slog.Error("provider error", "err", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close() // always close the upstream body

	// 3. Copy upstream headers to our response
	// This passes Content-Type: text/event-stream for SSE
	for key, vals := range upstream.Header {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(http.StatusOK)

	// 4. THE STREAMING MAGIC: pipe bytes as they arrive
	// io.Copy reads from upstream and writes to the client.
	// If the client supports flushing, each write is sent immediately.
	flusher, canFlush := w.(http.Flusher)

	buf := make([]byte, 4096)
	for {
		n, err := upstream.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush() // push bytes to client immediately
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("stream error", "err", err)
			break
		}
	}
}