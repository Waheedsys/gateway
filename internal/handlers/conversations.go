package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

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
}

func NewConversationHandler(conversations *repository.Conversation, messages *repository.MessageRepo, inferenceLogs *repository.InferenceLog, provider providers.Provider) *ConversationHandler {
	return &ConversationHandler{
		conversations: conversations,
		messages:      messages,
		inferenceLogs: inferenceLogs,
		provider:      provider,
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
}

type inferenceResponse struct {
	UserMessage      *models.Message      `json:"user_message"`
	AssistantMessage *models.Message      `json:"assistant_message,omitempty"`
	InferenceLog     *models.InferenceLog `json:"inference_log"`
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
	upstream, err := h.provider.Complete(r.Context(), req.Model, prompt, false)
	respondedAt := time.Now().UTC()
	latencyMs := int(respondedAt.Sub(requestedAt).Milliseconds())
	if err != nil {
		logEntry, _ := h.inferenceLogs.Create(r.Context(), &models.InferenceLog{
			ConversationID: conversationID,
			MessageID:      userMessage.ID,
			Provider:       h.provider.Name(),
			Model:          req.Model,
			Status:         "error",
			LatencyMs:      latencyMs,
			InputPreview:   preview(req.Content, 200),
			ErrorMessage:   err.Error(),
			RawMetadata:    map[string]any{"error": err.Error()},
			RequestedAt:    requestedAt,
			RespondedAt:    &respondedAt,
		})
		writeJSON(w, http.StatusBadGateway, inferenceResponse{UserMessage: userMessage, InferenceLog: logEntry})
		return
	}
	defer upstream.Body.Close()

	completion, err := h.provider.ParseResponse(upstream.Body)
	if completion == nil {
		completion = &providers.Completion{}
	}
	if err != nil {
		logEntry, _ := h.inferenceLogs.Create(r.Context(), &models.InferenceLog{
			ConversationID: conversationID,
			MessageID:      userMessage.ID,
			Provider:       h.provider.Name(),
			Model:          req.Model,
			Status:         "error",
			LatencyMs:      latencyMs,
			InputPreview:   preview(req.Content, 200),
			ErrorMessage:   err.Error(),
			RawMetadata:    completion.RawMetadata,
			RequestedAt:    requestedAt,
			RespondedAt:    &respondedAt,
		})
		writeJSON(w, http.StatusBadGateway, inferenceResponse{UserMessage: userMessage, InferenceLog: logEntry})
		return
	}

	assistantMessage, err := h.messages.Create(r.Context(), conversationID, models.RoleAssistant, completion.Text)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store assistant message")
		return
	}

	logEntry, err := h.inferenceLogs.Create(r.Context(), &models.InferenceLog{
		ConversationID:   conversationID,
		MessageID:        assistantMessage.ID,
		Provider:         h.provider.Name(),
		Model:            req.Model,
		Status:           "success",
		LatencyMs:        latencyMs,
		PromptTokens:     completion.Usage.InputTokens,
		CompletionTokens: completion.Usage.OutputTokens,
		TotalTokens:      completion.Usage.InputTokens + completion.Usage.OutputTokens,
		InputPreview:     preview(req.Content, 200),
		OutputPreview:    preview(completion.Text, 200),
		RawMetadata:      completion.RawMetadata,
		RequestedAt:      requestedAt,
		RespondedAt:      &respondedAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store inference log")
		return
	}

	writeJSON(w, http.StatusOK, inferenceResponse{
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
		InferenceLog:     logEntry,
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
