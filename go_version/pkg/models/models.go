package models

// Request is what your app sends to the gateway.
type Request struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
	Stream      bool    `json:"stream"`
}

// AnthropicRequest is the shape Anthropic's API expects.
type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []AnthropicMessage  `json:"messages"`
	Stream    bool               `json:"stream"`
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}