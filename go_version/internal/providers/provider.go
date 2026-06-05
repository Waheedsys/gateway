package providers

import (
	"context"
	"io"
	"net/http"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Completion struct {
	Text        string
	Usage       Usage
	RawMetadata map[string]any
}

// StreamChunk is one piece of text delivered during a streaming response.
// Done=true signals the stream has finished (equivalent to OpenRouter's [DONE]).
type StreamChunk struct {
	Text string
	Done bool
}

// Provider is the interface every LLM backend must satisfy.
// Complete sends a request; if stream=true the response body contains SSE lines.
// ParseStreamChunk reads one SSE "data: ..." line from a streaming response and
// converts it into a StreamChunk that the handler can forward to the client.
type Provider interface {
	Complete(ctx context.Context, model, prompt string, stream bool) (*http.Response, error)
	ParseResponse(body io.Reader) (*Completion, error)
	ParseStreamChunk(line string) (*StreamChunk, error)
	Name() string
	DefaultModel() string
}
