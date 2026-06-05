package models

import "time"

type InferenceLog struct {
    ID               string         `json:"id"`
    ConversationID   string         `json:"conversation_id"`
    MessageID        string         `json:"message_id"`
    Provider         string         `json:"provider"`
    Model            string         `json:"model"`
    Status           string         `json:"status"`
    LatencyMs        int            `json:"latency_ms"`
    PromptTokens     int            `json:"prompt_tokens"`
    CompletionTokens int            `json:"completion_tokens"`
    TotalTokens      int            `json:"total_tokens"`
    InputPreview     string         `json:"input_preview"`
    OutputPreview    string         `json:"output_preview"`
    ErrorMessage     string         `json:"error_message,omitempty"`
    RawMetadata      map[string]any `json:"raw_metadata"`
    RequestedAt      time.Time      `json:"requested_at"`
    RespondedAt      *time.Time     `json:"responded_at"`
}