package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func main() {
	root := "C:\\tmp"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	root, _ = filepath.Abs(root)

	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			return
		}

		var req request
		if err := json.Unmarshal(msg, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}

		switch req.Method {
		case "initialize":
			writeResponse(req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "ai-gateway-test-mcp",
					"version": "0.1.0",
				},
			})
		case "tools/list":
			writeResponse(req.ID, map[string]any{
				"tools": []map[string]any{
					{
						"name":        "echo",
						"description": "Echoes the input text.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"text": map[string]any{"type": "string"}},
						},
					},
					{
						"name":        "read_file",
						"description": "Reads a file from the configured test root.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"path": map[string]any{"type": "string"}},
						},
					},
					{
						"name":        "list_directory",
						"description": "Lists a directory from the configured test root.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"path": map[string]any{"type": "string"}},
						},
					},
					{
						"name":        "generate_poc_document",
						"description": "Runs the local POC DOCX generator and returns the generated document path.",
						"inputSchema": map[string]any{
							"type":       "object",
							"properties": map[string]any{},
						},
					},
					{
						"name":        "generate_document",
						"description": "Generates a general DOCX document from a topic, document type, audience, sections, and optional recommendations.",
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"topic":    map[string]any{"type": "string"},
								"title":    map[string]any{"type": "string"},
								"doc_type": map[string]any{"type": "string"},
								"audience": map[string]any{"type": "string"},
								"tone":     map[string]any{"type": "string"},
								"summary":  map[string]any{"type": "string"},
								"sections": map[string]any{
									"type": "array",
									"items": map[string]any{
										"oneOf": []map[string]any{
											{"type": "string"},
											{
												"type": "object",
												"properties": map[string]any{
													"heading": map[string]any{"type": "string"},
													"body":    map[string]any{"type": "string"},
													"bullets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
												},
											},
										},
									},
								},
								"recommendations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
						},
					},
				},
			})
		case "tools/call":
			result, err := callTool(root, req.Params)
			if err != nil {
				writeError(req.ID, -32000, err.Error())
				continue
			}
			writeResponse(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": result}},
			})
		default:
			writeError(req.ID, -32601, "method not found")
		}
	}
}

func callTool(root string, params json.RawMessage) (string, error) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return "", err
	}

	switch req.Name {
	case "echo":
		return fmt.Sprint(req.Arguments["text"]), nil
	case "read_file":
		path, err := safePath(root, fmt.Sprint(req.Arguments["path"]))
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case "list_directory":
		path, err := safePath(root, fmt.Sprint(req.Arguments["path"]))
		if err != nil {
			return "", err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return strings.Join(names, "\n"), nil
	case "generate_poc_document":
		return generatePOCDocument()
	case "generate_document":
		return generateDocument(req.Arguments)
	default:
		return "", fmt.Errorf("unknown tool %q", req.Name)
	}
}

func generatePOCDocument() (string, error) {
	command := strings.TrimSpace(os.Getenv("DOCGEN_COMMAND"))
	if command == "" {
		command = "python tools/build_poc_document.py"
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("DOCGEN_COMMAND is empty")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("document generation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		result = "docs/AI_Gateway_Elasticsearch_MCP_POC.docx"
	}
	abs, err := filepath.Abs(result)
	if err != nil {
		return result, nil
	}
	return abs, nil
}

func generateDocument(arguments map[string]any) (string, error) {
	if strings.TrimSpace(fmt.Sprint(arguments["topic"])) == "" && strings.TrimSpace(fmt.Sprint(arguments["title"])) == "" {
		return "", fmt.Errorf("topic or title is required")
	}

	command := strings.TrimSpace(os.Getenv("GENERAL_DOCGEN_COMMAND"))
	if command == "" {
		command = "python tools/build_general_document.py"
	}

	payload, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("GENERAL_DOCGEN_COMMAND is empty")
	}

	args := append(parts[1:], "--json", string(payload))
	cmd := exec.Command(parts[0], args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("general document generation failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return "", fmt.Errorf("document generator did not return an output path")
	}
	abs, err := filepath.Abs(result)
	if err != nil {
		return result, nil
	}
	return abs, nil
}

func safePath(root, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		requested = root
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(root, requested)
	}
	path, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path outside MCP root: %s", requested)
	}
	return path, nil
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	headers := http.Header{}
	for {
		line, err := reader.ReadString('\n')
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
		return nil, fmt.Errorf("invalid Content-Length")
	}
	data := make([]byte, length)
	_, err = io.ReadFull(reader, data)
	return data, err
}

func writeResponse(id any, result any) {
	writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeError(id any, code int, message string) {
	writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(msg any) {
	data, _ := json.Marshal(msg)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
}
