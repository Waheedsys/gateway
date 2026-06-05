// How it works:
//
//	HTTP Handler  ──publish──▶  Bus (buffered channel)  ──goroutine──▶  Subscriber
//
// The handler drops an event and returns immediately.
// The subscriber processes it asynchronously in the background.
package events

import "time"

// EventType is a string tag that identifies what happened.
// Using typed constants (not raw strings) means a typo is caught at compile time.
type EventType string

const (
	// EventInferenceCompleted fires when the provider returns a successful response.
	EventInferenceCompleted EventType = "inference.completed"

	// EventInferenceFailed fires when the provider call or parsing fails.
	EventInferenceFailed EventType = "inference.failed"
)

// Event is the envelope passed through the bus.
// Payload holds the event-specific data — subscribers cast it based on Type.
type Event struct {
	Type      EventType
	OccuredAt time.Time
	Payload   any
}

// InferenceCompletedPayload is the data attached to EventInferenceCompleted.
// It carries everything the subscriber needs to persist the result to the DB
// without re-calling the provider.
type InferenceCompletedPayload struct {
	ConversationID   string
	UserMessageID    string
	Provider         string
	Model            string
	LatencyMs        int
	InputPreview     string
	OutputPreview    string
	AssistantContent string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	RawMetadata      map[string]any
	RequestedAt      time.Time
	RespondedAt      time.Time
}

// InferenceFailedPayload is the data attached to EventInferenceFailed.
type InferenceFailedPayload struct {
	ConversationID string
	UserMessageID  string
	Provider       string
	Model          string
	LatencyMs      int
	InputPreview   string
	ErrorMessage   string
	RawMetadata    map[string]any
	RequestedAt    time.Time
	RespondedAt    time.Time
}
