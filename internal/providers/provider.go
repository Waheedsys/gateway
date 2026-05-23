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

type Provider interface {
	Complete(ctx context.Context, model, prompt string, stream bool) (*http.Response, error)
	ParseResponse(body io.Reader) (*Completion, error)
	Name() string
	DefaultModel() string
}
