package models

import "time"

type ConversationStatus string

const (
    StatusActive    ConversationStatus = "active"
    StatusCancelled ConversationStatus = "cancelled"
)

type Conversation struct {
    ID        string             `json:"id"`
    Title     string             `json:"title"`
    Status    ConversationStatus `json:"status"`
    CreatedAt time.Time          `json:"created_at"`
    UpdatedAt time.Time          `json:"updated_at"`
}