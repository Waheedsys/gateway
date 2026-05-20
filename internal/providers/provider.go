package providers

import (
	"context"
	"net/http"
)

// Provider is the interface every AI provider must implement.
// The proxy handler only knows about this interface —
// never about AnthropicProvider or OpenAIProvider directly.
type Provider interface {
	// Complete sends the request and returns the raw HTTP response.
	// We return *http.Response so we can stream it directly.
	Complete(ctx context.Context, model, prompt string, stream bool) (*http.Response, error)

	// Name identifies this provider in logs and metrics.
	Name() string
}