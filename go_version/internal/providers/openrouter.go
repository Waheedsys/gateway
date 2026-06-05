package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

type OpenRouterProvider struct {
	apiKey string
	client *http.Client
}

func NewOpenRouter(apiKey string) *OpenRouterProvider {
	return &OpenRouterProvider{
		apiKey: apiKey,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (o *OpenRouterProvider) Name() string { return "openrouter" }

func (o *OpenRouterProvider) DefaultModel() string { return "openrouter/free" }

func (o *OpenRouterProvider) Complete(ctx context.Context, model, prompt string, stream bool) (*http.Response, error) {
	body := openRouterRequest{
		Model:  model,
		Stream: stream,
		Messages: []openRouterMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("HTTP-Referer", "http://localhost:8080")
	req.Header.Set("X-Title", "AI Gateway Assignment")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call openrouter: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("openrouter returned %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

func (o *OpenRouterProvider) ParseResponse(body io.Reader) (*Completion, error) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read provider response: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return &Completion{RawMetadata: map[string]any{"raw_body": string(bodyBytes)}}, fmt.Errorf("parse provider response metadata: %w", err)
	}

	var parsed openRouterResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		return &Completion{RawMetadata: raw}, fmt.Errorf("parse provider response: %w", err)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return &Completion{
			Usage:       Usage{InputTokens: parsed.Usage.PromptTokens, OutputTokens: parsed.Usage.CompletionTokens},
			RawMetadata: raw,
		}, fmt.Errorf("provider response did not include text content")
	}

	return &Completion{
		Text: parsed.Choices[0].Message.Content,
		Usage: Usage{
			InputTokens:  parsed.Usage.PromptTokens,
			OutputTokens: parsed.Usage.CompletionTokens,
		},
		RawMetadata: raw,
	}, nil
}

// ParseStreamChunk parses one SSE line from a streaming OpenRouter response.
//
// OpenRouter sends lines in this format:
//
//	data: {"id":"...","choices":[{"delta":{"content":"Hello"}}]}
//	data: [DONE]
//
// We strip "data: ", detect [DONE], then unmarshal and pull out delta.content.
// An empty line (heartbeat) returns nil, nil — the caller should skip it.
func (o *OpenRouterProvider) ParseStreamChunk(line string) (*StreamChunk, error) {
	// SSE lines that carry data always start with "data: "
	if !strings.HasPrefix(line, "data: ") {
		return nil, nil // empty line or ":comment" — skip
	}
	payload := strings.TrimPrefix(line, "data: ")

	// [DONE] is the terminal marker sent by OpenRouter when the stream ends
	if strings.TrimSpace(payload) == "[DONE]" {
		return &StreamChunk{Done: true}, nil
	}

	var chunk openRouterStreamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return nil, fmt.Errorf("parse stream chunk: %w", err)
	}

	text := ""
	if len(chunk.Choices) > 0 {
		text = chunk.Choices[0].Delta.Content
	}

	return &StreamChunk{Text: text}, nil
}

// --- Request / response types ---

type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openRouterResponse is used for non-streaming (full) responses.
type openRouterResponse struct {
	Choices []struct {
		Message openRouterMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// openRouterStreamChunk is the shape of each SSE chunk during streaming.
// Note: streaming uses "delta" (a partial update) not "message" (full content).
type openRouterStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
