package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Client struct {
	command string
	nextID  atomic.Int64
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func NewFromEnv() *Client {
	return &Client{command: strings.TrimSpace(os.Getenv("MCP_SERVER_COMMAND"))}
}

func (c *Client) Enabled() bool {
	return c != nil && c.command != ""
}

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	session, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	defer session.close()

	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := session.request(ctx, "tools/list", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	session, err := c.open(ctx)
	if err != nil {
		return "", err
	}
	defer session.close()

	var raw json.RawMessage
	if err := session.request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, &raw); err != nil {
		return "", err
	}

	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err == nil {
		var b strings.Builder
		for _, item := range result.Content {
			if item.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(item.Text)
			}
		}
		if b.Len() > 0 {
			if result.IsError {
				return "", errors.New(b.String())
			}
			return b.String(), nil
		}
	}
	return string(raw), nil
}

func (c *Client) open(ctx context.Context) (*session, error) {
	parts := strings.Fields(c.command)
	if len(parts) == 0 {
		return nil, errors.New("MCP_SERVER_COMMAND is empty")
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s := &session{
		client: c,
		cmd:    cmd,
		in:     stdin,
		out:    bufio.NewReader(stdout),
	}

	var initResult json.RawMessage
	if err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "ai-gateway",
			"version": "0.1.0",
		},
	}, &initResult); err != nil {
		s.close()
		return nil, err
	}

	if err := s.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		s.close()
		return nil, err
	}

	return s, nil
}

type session struct {
	client *Client
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
}

func (s *session) request(ctx context.Context, method string, params any, dest any) error {
	id := s.client.nextID.Add(1)
	if err := s.write(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return err
	}

	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("mcp request %s timed out", method)
		default:
		}

		msg, err := s.readMessage()
		if err != nil {
			return err
		}
		var envelope struct {
			ID     any             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			return err
		}
		if fmt.Sprint(envelope.ID) != fmt.Sprint(id) {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("mcp %s failed: %s", method, envelope.Error.Message)
		}
		if dest == nil {
			return nil
		}
		return json.Unmarshal(envelope.Result, dest)
	}
}

func (s *session) notify(ctx context.Context, method string, params any) error {
	return s.write(ctx, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *session) write(ctx context.Context, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(data), data)
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(s.in, frame)
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (s *session) readMessage() ([]byte, error) {
	headers := http.Header{}
	for {
		line, err := s.out.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok {
			headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}

	length, err := strconv.Atoi(headers.Get("Content-Length"))
	if err != nil || length <= 0 {
		return nil, fmt.Errorf("invalid MCP Content-Length: %q", headers.Get("Content-Length"))
	}
	buf := make([]byte, length)
	_, err = io.ReadFull(s.out, buf)
	return bytes.TrimSpace(buf), err
}

func (s *session) close() {
	_ = s.in.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}
