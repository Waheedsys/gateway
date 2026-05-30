package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func Connect() (*Client, error) {
	baseURL := strings.TrimRight(os.Getenv("ELASTICSEARCH_URL"), "/")
	if baseURL == "" {
		baseURL = "http://localhost:9200"
	}

	c := &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}

	if err := c.ping(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) EnsureIndexes(ctx context.Context) error {
	indexes := map[string]map[string]any{
		"conversations": {
			"mappings": map[string]any{"properties": map[string]any{
				"id":         map[string]any{"type": "keyword"},
				"title":      map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
				"status":     map[string]any{"type": "keyword"},
				"created_at": map[string]any{"type": "date"},
				"updated_at": map[string]any{"type": "date"},
			}},
		},
		"messages": {
			"mappings": map[string]any{"properties": map[string]any{
				"id":              map[string]any{"type": "keyword"},
				"conversation_id": map[string]any{"type": "keyword"},
				"role":            map[string]any{"type": "keyword"},
				"content":         map[string]any{"type": "text"},
				"created_at":      map[string]any{"type": "date"},
			}},
		},
		"inference_logs": {
			"mappings": map[string]any{"properties": map[string]any{
				"id":                map[string]any{"type": "keyword"},
				"conversation_id":   map[string]any{"type": "keyword"},
				"message_id":        map[string]any{"type": "keyword"},
				"provider":          map[string]any{"type": "keyword"},
				"model":             map[string]any{"type": "keyword"},
				"status":            map[string]any{"type": "keyword"},
				"latency_ms":        map[string]any{"type": "integer"},
				"prompt_tokens":     map[string]any{"type": "integer"},
				"completion_tokens": map[string]any{"type": "integer"},
				"total_tokens":      map[string]any{"type": "integer"},
				"input_preview":     map[string]any{"type": "text"},
				"output_preview":    map[string]any{"type": "text"},
				"error_message":     map[string]any{"type": "text"},
				"raw_metadata":      map[string]any{"type": "object", "enabled": false},
				"requested_at":      map[string]any{"type": "date"},
				"responded_at":      map[string]any{"type": "date"},
			}},
		},
	}

	for index, body := range indexes {
		exists, err := c.indexExists(ctx, index)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := c.doJSON(ctx, http.MethodPut, "/"+index, body, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Index(ctx context.Context, index, id string, doc any) error {
	return c.doJSON(ctx, http.MethodPut, "/"+index+"/_doc/"+id+"?refresh=true", doc, nil)
}

func (c *Client) Get(ctx context.Context, index, id string, dest any) error {
	var res struct {
		Found  bool            `json:"found"`
		Source json.RawMessage `json:"_source"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/"+index+"/_doc/"+id, nil, &res); err != nil {
		return err
	}
	if !res.Found {
		return fmt.Errorf("%s/%s not found", index, id)
	}
	return json.Unmarshal(res.Source, dest)
}

func (c *Client) Delete(ctx context.Context, index, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/"+index+"/_doc/"+id+"?refresh=true", nil, nil)
}

func (c *Client) Update(ctx context.Context, index, id string, partial any) error {
	return c.doJSON(ctx, http.MethodPost, "/"+index+"/_update/"+id+"?refresh=true", map[string]any{"doc": partial}, nil)
}

func (c *Client) Search(ctx context.Context, index string, query any, dest any) error {
	return c.doJSON(ctx, http.MethodPost, "/"+index+"/_search", query, dest)
}

func (c *Client) ping(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodGet, "/", nil, nil)
}

func (c *Client) indexExists(ctx context.Context, index string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.baseURL+"/"+index, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("elasticsearch HEAD %s failed: %s", index, resp.Status)
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("elasticsearch %s %s failed: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if dest != nil && len(data) > 0 {
		if err := json.Unmarshal(data, dest); err != nil {
			return err
		}
	}
	return nil
}
