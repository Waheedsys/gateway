# AI Agent Gateway (Python/FastAPI)

A Python FastAPI LLM Agent Gateway with LangGraph-powered autonomous tool calling, Elasticsearch-backed memory, SSE streaming, Redis response caching, and inference observability.

## Features

- Multi-turn conversation API with persisted user and assistant messages
- Elasticsearch document storage for conversations, messages, and inference logs
- Context assembly from recent chat history plus Elasticsearch full-text retrieval
- **LangGraph ReAct Agent** with automatic tool-calling loop (think → act → observe → respond)
- OpenRouter and Anthropic provider support through LangChain
- Streaming and non-streaming inference responses (SSE)
- Redis-backed short-lived inference response cache
- Async event bus for background inference log persistence
- External inference log ingestion endpoint
- 24-hour inference statistics endpoint
- Optional stdio MCP server integration — tools are automatically bound to the LangGraph agent

## Architecture

```
Client
  -> FastAPI Gateway
      -> Elasticsearch
          - conversations
          - messages
          - inference_logs
          - BM25-style message retrieval
      -> Redis
          - short-lived inference response cache
      -> LangGraph ReAct Agent
          -> MCP Server (optional stdio tool discovery)
          -> LLM Provider (OpenRouter or Anthropic via LangChain)
      -> Async Event Bus
          - background inference log writes
```

## Setup

Create or update `.env`:

```env
ELASTICSEARCH_URL=http://localhost:9200
REDIS_ADDR=localhost:6379
LLM_PROVIDER=openrouter
OPENROUTER_API_KEY=your-openrouter-key
ANTHROPIC_API_KEY=
MCP_SERVER_COMMAND=
CACHE_TTL=5m
```

Supported `LLM_PROVIDER` values: `openrouter`, `anthropic`

## Running

### Docker (Full Stack)
```bash
docker compose up -d --build
```

### Local Development (dependencies via Docker, app runs directly)
```bash
docker compose up -d elasticsearch redis kibana
pip install -r requirements.txt
uvicorn main:app --host 0.0.0.0 --port 8080 --reload
```

## Local Services

| Service | URL | Purpose |
| --- | --- | --- |
| Gateway | `http://localhost:8080` | FastAPI API server |
| Elasticsearch | `http://localhost:9200` | Document storage and context retrieval |
| Kibana | `http://localhost:5601` | Elasticsearch inspection UI |
| Redis | `localhost:6379` | Inference response cache |

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
| `POST` | `/conversations/{id}/infer` | Run agent inference (ReAct loop + MCP tools) |
| `GET` | `/conversations/{id}/inference-logs` | List inference logs for one conversation |
| `POST` | `/ingest/inference-logs` | Ingest external inference logs |
| `GET` | `/inference/stats` | Get 24-hour inference stats |
| `GET` | `/mcp/tools` | List MCP tools |
| `POST` | `/mcp/tools/call` | Call one MCP tool |

## Example Requests

Create a conversation:
```bash
curl -X POST http://localhost:8080/conversations \
  -H "Content-Type: application/json" \
  -d '{"title": "Agent demo"}'
```

Run non-streaming inference:
```bash
curl -X POST http://localhost:8080/conversations/CONVERSATION_ID/infer \
  -H "Content-Type: application/json" \
  -d '{"content": "What is the capital of France?", "stream": false}'
```

Run streaming inference:
```bash
curl -X POST http://localhost:8080/conversations/CONVERSATION_ID/infer \
  -H "Content-Type: application/json" \
  -d '{"content": "Explain quantum computing briefly.", "stream": true}'
```

## Agent Loop

When `/conversations/{id}/infer` is called, the gateway:

1. Stores the user message in Elasticsearch.
2. Loads the last 8 messages from the conversation as chat history.
3. Searches for the top 5 semantically relevant messages via Elasticsearch BM25.
4. Builds a system prompt incorporating the retrieved context.
5. Fetches available MCP tools (if `MCP_SERVER_COMMAND` is set) and binds them to the agent.
6. Runs a **LangGraph ReAct agent loop**:
   - The LLM reasons about the user's request.
   - If a tool is needed, it calls the MCP server automatically.
   - Observes tool output and reasons further.
   - Returns a final answer.
7. Stores the assistant response in Elasticsearch.
8. Caches the response in Redis.
9. Publishes an inference event for async log persistence.

## MCP Tool Calling

Set `MCP_SERVER_COMMAND` to a stdio MCP server:

```env
MCP_SERVER_COMMAND=npx -y @modelcontextprotocol/server-filesystem C:\tmp
```

Tools are automatically discovered and bound to the agent — no manual `tool:` prefix needed.

## Go Version

The original Go implementation is preserved in `go_version/` for reference.

## Notes

- Elasticsearch is the source of truth for conversations, messages, and inference logs.
- Message relevance search uses Elasticsearch BM25 full-text search.
- Redis is used for short-lived inference response caching via `CACHE_TTL`.
- Authentication is not currently enforced on any routes.
