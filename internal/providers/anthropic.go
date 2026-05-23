package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func (a *AnthropicProvider) DefaultModel() string { return "claude-sonnet-4-5" }

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

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage AnthropicUsage `json:"usage"`
}

func (a *AnthropicProvider) ParseResponse(body io.Reader) (*Completion, error) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return &Completion{RawMetadata: map[string]any{"raw_body": string(bodyBytes)}}, fmt.Errorf("parse provider response metadata: %w", err)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &Completion{RawMetadata: raw}, fmt.Errorf("parse provider response: %w", err)
	}

	var output bytes.Buffer
	for _, block := range parsed.Content {
		if block.Type == "text" || block.Type == "" {
			output.WriteString(block.Text)
		}
	}
	if output.Len() == 0 {
		return &Completion{
			Usage:       Usage{InputTokens: parsed.Usage.InputTokens, OutputTokens: parsed.Usage.OutputTokens},
			RawMetadata: raw,
		}, fmt.Errorf("provider response did not include text content")
	}

	return &Completion{
		Text:        output.String(),
		Usage:       Usage{InputTokens: parsed.Usage.InputTokens, OutputTokens: parsed.Usage.OutputTokens},
		RawMetadata: raw,
	}, nil
}
