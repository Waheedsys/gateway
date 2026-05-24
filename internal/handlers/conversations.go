package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/events"
	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/providers"
	"github.com/Waheedsys/ai-gateway/internal/repository"
	"github.com/go-chi/chi/v5"
)

type ConversationHandler struct {
	conversations *repository.Conversation
	messages      *repository.MessageRepo
	inferenceLogs *repository.InferenceLog
	provider      providers.Provider
	bus           *events.Bus
}

func NewConversationHandler(conversations *repository.Conversation, messages *repository.MessageRepo, inferenceLogs *repository.InferenceLog, provider providers.Provider, bus *events.Bus) *ConversationHandler {
	return &ConversationHandler{
		conversations: conversations,
		messages:      messages,
		inferenceLogs: inferenceLogs,
		provider:      provider,
		bus:           bus,
	}
}

func (h *ConversationHandler) Routes(r chi.Router) {
	r.Get("/conversations", h.ListConversations)
	r.Post("/conversations", h.CreateConversation)
	r.Get("/conversations/{conversationID}", h.GetConversation)
	r.Post("/conversations/{conversationID}/cancel", h.CancelConversation)
	r.Get("/conversations/{conversationID}/messages", h.ListMessages)
	r.Post("/conversations/{conversationID}/messages", h.CreateMessage)
	r.Post("/conversations/{conversationID}/infer", h.RunInference)
	r.Get("/conversations/{conversationID}/inference-logs", h.ListInferenceLogs)
}

type createConversationRequest struct {
	Title string `json:"title"`
}

type createMessageRequest struct {
	Role    models.MessageRole `json:"role"`
	Content string             `json:"content"`
}

type inferenceRequest struct {
	Content string `json:"content"`
	Model   string `json:"model"`
	Stream  *bool  `json:"stream,omitempty"`
}

type inferenceResponse struct {
	UserMessage      *models.Message `json:"user_message"`
	AssistantMessage *models.Message `json:"assistant_message,omitempty"`
}

func (h *ConversationHandler) CreateConversation(w http.ResponseWriter, r *http.Request) {
	var req createConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		req.Title = "New conversation"
	}

	conversation, err := h.conversations.Create(r.Context(), req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create conversation")
		return
	}

	writeJSON(w, http.StatusCreated, conversation)
}

func (h *ConversationHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	conversations, err := h.conversations.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list conversations")
		return
	}

	writeJSON(w, http.StatusOK, conversations)
}

func (h *ConversationHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	conversation, err := h.conversations.GetByID(r.Context(), conversationID)
	if err != nil {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}

	writeJSON(w, http.StatusOK, conversation)
}

func (h *ConversationHandler) CancelConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	if err := h.conversations.Cancel(r.Context(), conversationID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel conversation")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ConversationHandler) CreateMessage(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	var req createMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "role and content are required")
		return
	}

	message, err := h.messages.Create(r.Context(), conversationID, req.Role, req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create message")
		return
	}

	writeJSON(w, http.StatusCreated, message)
}

func (h *ConversationHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	messages, err := h.messages.ListByConversation(r.Context(), conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list messages")
		return
	}

	writeJSON(w, http.StatusOK, messages)
}

func (h *ConversationHandler) RunInference(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	conversation, err := h.conversations.GetByID(r.Context(), conversationID)
	if err != nil {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	if conversation.Status == models.StatusCancelled {
		writeError(w, http.StatusConflict, "conversation is cancelled")
		return
	}

	var req inferenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.Model == "" {
		req.Model = h.provider.DefaultModel()
	}

	stream := true
	if req.Stream != nil {
		stream = *req.Stream
	}

	userMessage, err := h.messages.Create(r.Context(), conversationID, models.RoleUser, req.Content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store user message")
		return
	}

	history, err := h.messages.GetLastN(r.Context(), conversationID, 8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load conversation context")
		return
	}

	prompt := buildContextPrompt(history)
	requestedAt := time.Now().UTC()

	if stream {
		upstream, err := h.provider.Complete(r.Context(), req.Model, prompt, true)
		respondedAt := time.Now().UTC()
		latencyMs := int(respondedAt.Sub(requestedAt).Milliseconds())
		if err != nil {
			h.bus.Publish(events.Event{
				Type:      events.EventInferenceFailed,
				OccuredAt: time.Now().UTC(),
				Payload: events.InferenceFailedPayload{
					ConversationID: conversationID,
					UserMessageID:  userMessage.ID,
					Provider:       h.provider.Name(),
					Model:          req.Model,
					LatencyMs:      latencyMs,
					InputPreview:   preview(req.Content, 200),
					ErrorMessage:   err.Error(),
					RawMetadata:    map[string]any{"error": err.Error()},
					RequestedAt:    requestedAt,
					RespondedAt:    respondedAt,
				},
			})
			fmt.Println("-------", err)
			writeJSON(w, http.StatusBadGateway, inferenceResponse{UserMessage: userMessage})
			return
		}
		defer upstream.Body.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		flusher, canFlush := w.(http.Flusher)

		// Send initial user message event
		startPayload, _ := json.Marshal(map[string]any{
			"event":        "start",
			"user_message": userMessage,
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", startPayload)
		if canFlush {
			flusher.Flush()
		}

		scanner := bufio.NewScanner(upstream.Body)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // up to 1MB lines
		var accumulatedText strings.Builder
		var scanErr error

		for scanner.Scan() {
			line := scanner.Text()
			line = strings.TrimSuffix(line, "\r")

			chunk, err := h.provider.ParseStreamChunk(line)
			if err != nil {
				// skip or report error
				continue
			}
			if chunk == nil {
				continue
			}
			if chunk.Done {
				break
			}
			if chunk.Text != "" {
				accumulatedText.WriteString(chunk.Text)
				textPayload, _ := json.Marshal(map[string]any{
					"event": "text",
					"text":  chunk.Text,
				})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", textPayload)
				if canFlush {
					flusher.Flush()
				}
			}
		}

		if err := scanner.Err(); err != nil {
			scanErr = err
		}

		responseText := accumulatedText.String()
		respondedAt = time.Now().UTC()
		latencyMs = int(respondedAt.Sub(requestedAt).Milliseconds())

		if scanErr != nil {
			h.bus.Publish(events.Event{
				Type:      events.EventInferenceFailed,
				OccuredAt: time.Now().UTC(),
				Payload: events.InferenceFailedPayload{
					ConversationID: conversationID,
					UserMessageID:  userMessage.ID,
					Provider:       h.provider.Name(),
					Model:          req.Model,
					LatencyMs:      latencyMs,
					InputPreview:   preview(req.Content, 200),
					ErrorMessage:   scanErr.Error(),
					RawMetadata:    map[string]any{"error": scanErr.Error()},
					RequestedAt:    requestedAt,
					RespondedAt:    respondedAt,
				},
			})
			errorPayload, _ := json.Marshal(map[string]any{
				"event": "error",
				"error": scanErr.Error(),
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", errorPayload)
			if canFlush {
				flusher.Flush()
			}
			return
		}

		assistantMessage, err := h.messages.Create(r.Context(), conversationID, models.RoleAssistant, responseText)
		if err != nil {
			errorPayload, _ := json.Marshal(map[string]any{
				"event": "error",
				"error": "failed to store assistant message",
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", errorPayload)
			if canFlush {
				flusher.Flush()
			}
			return
		}

		h.bus.Publish(events.Event{
			Type:      events.EventInferenceCompleted,
			OccuredAt: time.Now().UTC(),
			Payload: events.InferenceCompletedPayload{
				ConversationID:   conversationID,
				UserMessageID:    userMessage.ID,
				Provider:         h.provider.Name(),
				Model:            req.Model,
				LatencyMs:        latencyMs,
				InputPreview:     preview(req.Content, 200),
				OutputPreview:    preview(responseText, 200),
				AssistantContent: responseText,
				PromptTokens:     0,
				CompletionTokens: 0,
				TotalTokens:      0,
				RawMetadata:      map[string]any{"streaming": true},
				RequestedAt:      requestedAt,
				RespondedAt:      respondedAt,
			},
		})

		donePayload, _ := json.Marshal(map[string]any{
			"event":             "done",
			"assistant_message": assistantMessage,
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", donePayload)
		if canFlush {
			flusher.Flush()
		}
		return
	}

	upstream, err := h.provider.Complete(r.Context(), req.Model, prompt, false)
	respondedAt := time.Now().UTC()
	latencyMs := int(respondedAt.Sub(requestedAt).Milliseconds())
	if err != nil {
		h.bus.Publish(events.Event{
			Type:      events.EventInferenceFailed,
			OccuredAt: time.Now().UTC(),
			Payload: events.InferenceFailedPayload{
				ConversationID: conversationID,
				UserMessageID:  userMessage.ID,
				Provider:       h.provider.Name(),
				Model:          req.Model,
				LatencyMs:      latencyMs,
				InputPreview:   preview(req.Content, 200),
				ErrorMessage:   err.Error(),
				RawMetadata:    map[string]any{"error": err.Error()},
				RequestedAt:    requestedAt,
				RespondedAt:    respondedAt,
			},
		})
		fmt.Println("-------", err)
		writeJSON(w, http.StatusBadGateway, inferenceResponse{UserMessage: userMessage})
		return
	}
	defer upstream.Body.Close()

	completion, err := h.provider.ParseResponse(upstream.Body)
	if completion == nil {
		completion = &providers.Completion{}
	}
	if err != nil {
		h.bus.Publish(events.Event{
			Type:      events.EventInferenceFailed,
			OccuredAt: time.Now().UTC(),
			Payload: events.InferenceFailedPayload{
				ConversationID: conversationID,
				UserMessageID:  userMessage.ID,
				Provider:       h.provider.Name(),
				Model:          req.Model,
				LatencyMs:      latencyMs,
				InputPreview:   preview(req.Content, 200),
				ErrorMessage:   err.Error(),
				RawMetadata:    map[string]any{"error": err.Error()},
				RequestedAt:    requestedAt,
				RespondedAt:    respondedAt,
			},
		})
		fmt.Println("-------", err)
		writeJSON(w, http.StatusBadGateway, inferenceResponse{UserMessage: userMessage})
		return
	}

	assistantMessage, err := h.messages.Create(r.Context(), conversationID, models.RoleAssistant, completion.Text)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store assistant message")
		return
	}

	h.bus.Publish(events.Event{
		Type:      events.EventInferenceCompleted,
		OccuredAt: time.Now().UTC(),
		Payload: events.InferenceCompletedPayload{
			ConversationID:   conversationID,
			UserMessageID:    userMessage.ID,
			Provider:         h.provider.Name(),
			Model:            req.Model,
			LatencyMs:        latencyMs,
			InputPreview:     preview(req.Content, 200),
			OutputPreview:    preview(completion.Text, 200),
			AssistantContent: completion.Text,
			PromptTokens:     completion.Usage.InputTokens,
			CompletionTokens: completion.Usage.OutputTokens,
			TotalTokens:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
			RawMetadata:      completion.RawMetadata,
			RequestedAt:      requestedAt,
			RespondedAt:      respondedAt,
		},
	})

	writeJSON(w, http.StatusOK, inferenceResponse{
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
	})
}

func (h *ConversationHandler) ListInferenceLogs(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "conversationID")

	logs, err := h.inferenceLogs.ListByConversation(r.Context(), conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inference logs")
		return
	}

	writeJSON(w, http.StatusOK, logs)
}

func buildContextPrompt(messages []models.Message) string {
	var b strings.Builder
	b.WriteString("You are a helpful assistant. Continue this conversation using the recent context.\n\n")
	for _, msg := range messages {
		b.WriteString(string(msg.Role))
		b.WriteString(": ")
		b.WriteString(msg.Content)
		b.WriteString("\n")
	}
	b.WriteString("\nassistant:")
	return b.String()
}

func preview(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
