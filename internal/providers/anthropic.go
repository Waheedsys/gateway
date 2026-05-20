package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Waheedsys/ai-gateway/pkg/models"
)

const anthropicURL = "https://api.anthropic.com/v1/messages"

type AnthropicProvider struct {
	apiKey string
	client *http.Client
}

// New creates a provider. Call this once at startup.
func NewAnthropic(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		client: &http.Client{
			Timeout: 120 * time.Second, // long timeout for big responses
		},
	}
}

func (a *AnthropicProvider) Name() string { return "anthropic" }

func (a *AnthropicProvider) Complete(
	ctx context.Context,
	model, prompt string,
	stream bool,
) (*http.Response, error) {

	// 1. Build the Anthropic-shaped request body
	body := models.AnthropicRequest{
		Model:     model,
		MaxTokens: 1024,
		Stream:    stream,
		Messages: []models.AnthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 2. Build the HTTP request to Anthropic
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		anthropicURL,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	// 3. Set required headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// 4. Fire the request — body stays open for streaming
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call anthropic: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic returned %d", resp.StatusCode)
	}

	// Return the response — caller must close resp.Body
	return resp, nil
}