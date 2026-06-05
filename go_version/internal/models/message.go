package models

import "time"

type MessageRole string

const (
    RoleUser      MessageRole = "user"
    RoleAssistant MessageRole = "assistant"
    RoleSystem    MessageRole = "system"
)

type Message struct {
    ID             string      `json:"id"`
    ConversationID string      `json:"conversation_id"`
    Role           MessageRole `json:"role"`
    Content        string      `json:"content"`
    CreatedAt      time.Time   `json:"created_at"`
}