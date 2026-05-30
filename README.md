# AI Gateway

A lightweight Go LLM gateway for multi-turn conversations, Elasticsearch-backed memory, SSE streaming, provider abstraction, MCP tool calling, Redis response caching, and inference observability.

## Features

- Multi-turn conversation API with persisted user and assistant messages
- Elasticsearch document storage for conversations, messages, and inference logs
- Context assembly from recent chat history plus Elasticsearch full-text retrieval
- OpenRouter and Anthropic provider support behind a common interface
- Streaming and non-streaming inference responses
- Redis-backed short-lived inference response cache
- In-process event bus for asynchronous inference log persistence
- External inference log ingestion endpoint
- 24-hour inference statistics endpoint
- Optional stdio MCP server integration for tool discovery, direct tool calls, and inline tool context
- Kibana service for inspecting Elasticsearch documents locally
- JWT auth and Redis rate-limit middleware packages are included, but not currently wired into `cmd/gateway/main.go`

## Architecture

```text
Client
  -> Go Gateway
      -> Elasticsearch
          - conversations
          - messages
          - inference_logs
          - BM25-style message retrieval
      -> Redis
          - short-lived inference response cache
      -> Event Bus
          - async success/error inference log writes
      -> MCP server
          - optional stdio tool discovery and tool execution
      -> LLM provider
          - OpenRouter or Anthropic
```

The gateway stores each conversation, message, and inference log as an Elasticsearch document. During inference, it builds the model prompt dynamically from recent conversation history, relevant messages found through Elasticsearch full-text search, and optional MCP tool output.

## Context Flow

When `POST /conversations/{id}/infer` is called, the gateway:

1. Stores the incoming user message in the `messages` index.
2. Loads the latest 8 messages from the same conversation.
3. Searches the same conversation for the top 5 relevant messages using Elasticsearch `match` on `content`.
4. Optionally detects an inline MCP tool call such as `tool:read_file {...}` and adds the tool result.
5. Builds a prompt containing retrieved context, tool context, recent messages, and the final `assistant:` marker.
6. Calls the selected provider.
7. Stores the assistant response in the `messages` index.
8. Publishes an inference success or failure event.
9. Persists the inference log asynchronously into `inference_logs`.

Current retrieval is text/BM25 based. Vector embeddings are not required for the current implementation, but they would be a natural future upgrade for semantic recall.

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
CACHE_TTL=5m
```

Supported `LLM_PROVIDER` values:

```text
openrouter
anthropic
```

Run the full local stack:

```bash
docker compose up -d --build
```

Run only dependencies and start Go locally:

```bash
docker compose up -d elasticsearch redis kibana
go run ./cmd/gateway
```

The gateway creates required Elasticsearch indexes automatically at startup.

## Local Services

| Service | URL | Purpose |
| --- | --- | --- |
| Gateway | `http://localhost:8080` | API server |
| Elasticsearch | `http://localhost:9200` | Document storage and context retrieval |
| Kibana | `http://localhost:5601` | Elasticsearch inspection UI |
| Redis | `localhost:6379` | Inference response cache |

## Elasticsearch Indexes

Indexes are created by `EnsureIndexes` during gateway startup.

| Index | Purpose |
| --- | --- |
| `conversations` | Conversation title, status, and timestamps |
| `messages` | User, assistant, and system messages |
| `inference_logs` | Provider, model, latency, token usage, status, previews, and raw metadata |

Documents are written with `PUT /{index}/_doc/{id}?refresh=true`, so newly created messages are immediately searchable in local development.

## MCP Tool Calling

Set `MCP_SERVER_COMMAND` to a stdio MCP server command. Example:

```env
MCP_SERVER_COMMAND=npx -y @modelcontextprotocol/server-filesystem C:\tmp
```

List tools:

```bash
curl.exe http://localhost:8080/mcp/tools
```

Call a tool directly:

```bash
curl.exe -X POST http://localhost:8080/mcp/tools/call ^
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

## API Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Health check |
| `POST` | `/conversations` | Create a conversation |
| `GET` | `/conversations` | List conversations |
| `GET` | `/conversations/{id}` | Load conversation state |
| `POST` | `/conversations/{id}/cancel` | Mark a conversation as cancelled |
| `GET` | `/conversations/{id}/messages` | List conversation messages |
| `POST` | `/conversations/{id}/messages` | Add a message manually |
| `POST` | `/conversations/{id}/infer` | Store user message, build context, call provider, store assistant message |
| `GET` | `/conversations/{id}/inference-logs` | List inference logs for one conversation |
| `POST` | `/ingest/inference-logs` | Ingest external inference logs |
| `GET` | `/inference/stats` | Get 24-hour inference stats |
| `GET` | `/mcp/tools` | List MCP tools |
| `POST` | `/mcp/tools/call` | Call one MCP tool |

## Example Requests

Create a conversation:

```bash
curl.exe -X POST http://localhost:8080/conversations ^
  -H "Content-Type: application/json" ^
  -d "{\"title\":\"Gateway demo\"}"
```

Run non-streaming inference:

```bash
curl.exe -X POST http://localhost:8080/conversations/CONVERSATION_ID/infer ^
  -H "Content-Type: application/json" ^
  -d "{\"content\":\"Remember that our preferred database is Elasticsearch.\",\"model\":\"openrouter/free\",\"stream\":false}"
```

Run streaming inference:

```bash
curl.exe -X POST http://localhost:8080/conversations/CONVERSATION_ID/infer ^
  -H "Content-Type: application/json" ^
  -d "{\"content\":\"What database did I prefer?\",\"stream\":true}"
```

## Streaming

When `/conversations/{id}/infer` is called with `"stream": true`, the gateway returns `text/event-stream` chunks:

```text
data: {"event":"start","user_message":{...}}
data: {"event":"text","text":"partial text"}
data: {"event":"done","assistant_message":{...}}
```

Cached responses are also returned as SSE when streaming is requested. The gateway emits the cached text in small chunks and still stores an assistant message for the turn.

## Inference Logs And Stats

Successful and failed provider calls publish events to an in-process event bus. A background subscriber persists those events into the `inference_logs` Elasticsearch index.

External services can also write compatible logs:

```bash
curl.exe -X POST http://localhost:8080/ingest/inference-logs ^
  -H "Content-Type: application/json" ^
  -d "{\"provider\":\"external\",\"model\":\"demo\",\"status\":\"success\",\"latency_ms\":120,\"input_preview\":\"hello\",\"output_preview\":\"world\"}"
```

Get 24-hour stats:

```bash
curl.exe http://localhost:8080/inference/stats
```

## Authentication Note

The repository includes JWT helpers in `internal/auth` and a Redis token-bucket middleware in `internal/ratelimiter`. The current gateway router does not attach these middleware, so the listed API endpoints run without auth in the present code path.

You can still generate a local JWT for development experiments:

```bash
go run ./cmd/gentoken
```

## Notes

- PostgreSQL has been removed from the runtime path.
- Elasticsearch is the source of truth for conversations, messages, and inference logs.
- Message relevance search currently uses Elasticsearch full-text search, not embeddings.
- Redis is used for short-lived inference response caching through `CACHE_TTL`.
- MCP support is stdio-based and configured through `MCP_SERVER_COMMAND`.
