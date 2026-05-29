# AI Gateway

A lightweight Go LLM gateway for multi-turn conversations, SSE streaming, Elasticsearch-backed context retrieval, inference logging, Redis rate limiting, and MCP tool calling.

## Architecture

```text
Client
  -> Go Gateway
      -> Elasticsearch: conversations, messages, inference logs, retrieved context
      -> Redis: rate limiting and short-lived inference cache
      -> MCP server: optional tool discovery and tool calls
      -> LLM provider: OpenRouter or Anthropic
```

The gateway stores conversations, messages, and inference logs as Elasticsearch documents. During inference it builds context from the latest messages plus relevant messages found with Elasticsearch full-text search. If an MCP server is configured, the gateway can list tools, call tools directly, and inject inline tool-call results into the prompt.

## Setup

Create or update `.env`:

```env
ELASTICSEARCH_URL=http://localhost:9200
REDIS_ADDR=localhost:6379
JWT_SECRET=supersecretjwtkey123!
LLM_PROVIDER=openrouter
OPENROUTER_API_KEY=your-openrouter-key
ANTHROPIC_API_KEY=
MCP_SERVER_COMMAND=
```

Run the full stack:

```bash
docker compose up -d --build
```

Run only dependencies and start Go locally:

```bash
docker compose up -d elasticsearch redis
go run ./cmd/gateway
```

The gateway creates required Elasticsearch indexes automatically at startup.

## MCP Tool Calling

Set `MCP_SERVER_COMMAND` to a stdio MCP server command. Example:

```env
MCP_SERVER_COMMAND=npx -y @modelcontextprotocol/server-filesystem C:\tmp
```

List tools:

```bash
curl.exe http://localhost:8080/mcp/tools -H "Authorization: Bearer YOUR_JWT_TOKEN"
```

Call a tool directly:

```bash
curl.exe -X POST http://localhost:8080/mcp/tools/call ^
  -H "Authorization: Bearer YOUR_JWT_TOKEN" ^
  -H "Content-Type: application/json" ^
  -d "{\"name\":\"read_file\",\"arguments\":{\"path\":\"C:\\tmp\\note.txt\"}}"
```

Call a tool inside inference by prefixing the user message:

```json
{
  "content": "tool:read_file {\"path\":\"C:\\tmp\\note.txt\"}",
  "stream": false
}
```

The gateway calls the MCP tool, adds the result to the model context, then asks the configured LLM provider to answer.

## Authentication

Protected endpoints require a bearer JWT signed with `JWT_SECRET`.

Generate a local test token:

```bash
go run ./cmd/gentoken
```

Use it in requests:

```text
Authorization: Bearer <token>
```

## Core API Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET` | `/health` | No | Health check |
| `POST` | `/conversations` | Yes | Create a conversation |
| `GET` | `/conversations` | Yes | List conversations |
| `GET` | `/conversations/{id}` | Yes | Load conversation state |
| `POST` | `/conversations/{id}/cancel` | Yes | Mark a conversation as cancelled |
| `GET` | `/conversations/{id}/messages` | Yes | List conversation messages |
| `POST` | `/conversations/{id}/messages` | Yes | Add a message |
| `POST` | `/conversations/{id}/infer` | Yes | Store user message, retrieve context, optionally call MCP, invoke LLM |
| `GET` | `/conversations/{id}/inference-logs` | Yes | List inference logs |
| `POST` | `/ingest/inference-logs` | Yes | Ingest external inference logs |
| `GET` | `/inference/stats` | Yes | Get 24-hour inference stats |
| `GET` | `/mcp/tools` | Yes | List MCP tools |
| `POST` | `/mcp/tools/call` | Yes | Call one MCP tool |

## Streaming

When `/conversations/{id}/infer` is called with `"stream": true`, the gateway returns `text/event-stream` chunks:

```text
data: {"event":"start","user_message":{...}}
data: {"event":"text","text":"partial text"}
data: {"event":"done","assistant_message":{...}}
```

## Notes

- PostgreSQL has been removed from the runtime path.
- Elasticsearch is currently used for document storage and BM25-style context retrieval.
- Redis remains in place for rate limiting and short-lived response caching.
- MCP support is stdio-based and configured through `MCP_SERVER_COMMAND`.
