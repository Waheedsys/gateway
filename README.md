# AI Gateway

Lightweight LLM gateway for multi-turn conversations, inference logging, and log ingestion.

## Setup

1. Start dependencies:

```powershell
docker compose up -d
```

2. Create `.env`:

```env
DATABASE_URL=postgres://postgres:postgres@localhost:5433/ai_gateway?sslmode=disable
REDIS_ADDR=localhost:6379
JWT_SECRET=replace-me
LLM_PROVIDER=openrouter
OPENROUTER_API_KEY=your-openrouter-key
```

The default OpenRouter model is `openrouter/free`. You can override it per request with the `model` field. OpenRouter free models can change over time, so use any currently available model ID ending in `:free`.

3. Run the API:

```powershell
go run ./cmd/gateway
```

Migrations run automatically at startup.

## Auth

Protected endpoints require a bearer JWT signed with `JWT_SECRET`. A helper exists at `cmd/gentoken`.

```powershell
go run ./cmd/gentoken
```

Use the returned token as:

```text
Authorization: Bearer <token>
```

## Core Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Health check |
| `POST` | `/conversations` | Create a conversation |
| `GET` | `/conversations` | List conversations |
| `GET` | `/conversations/{id}` | Resume/load one conversation |
| `POST` | `/conversations/{id}/cancel` | Cancel a conversation |
| `GET` | `/conversations/{id}/messages` | List chat messages |
| `POST` | `/conversations/{id}/infer` | Store user message, call the configured LLM provider, store assistant message, store inference log |
| `GET` | `/conversations/{id}/inference-logs` | List logs for one conversation |
| `POST` | `/ingest/inference-logs` | Ingest SDK/wrapper inference logs |
| `GET` | `/inference/stats` | Last 24h latency/token/error summary |

## Demo Requests

Create a conversation:

```powershell
curl.exe -X POST http://localhost:8080/conversations `
  -H "Authorization: Bearer <token>" `
  -H "Content-Type: application/json" `
  -d "{\"title\":\"Demo chat\"}"
```

Run inference:

```powershell
curl.exe -X POST http://localhost:8080/conversations/<conversation_id>/infer `
  -H "Authorization: Bearer <token>" `
  -H "Content-Type: application/json" `
  -d "{\"content\":\"Explain inference logging in one paragraph.\",\"model\":\"openrouter/free\"}"
```

Ingest a standalone log:

```powershell
curl.exe -X POST http://localhost:8080/ingest/inference-logs `
  -H "Authorization: Bearer <token>" `
  -H "Content-Type: application/json" `
  -d "{\"provider\":\"openrouter\",\"model\":\"openrouter/free\",\"status\":\"success\",\"latency_ms\":720,\"prompt_tokens\":12,\"completion_tokens\":32,\"total_tokens\":44,\"input_preview\":\"hello\",\"output_preview\":\"hi there\",\"raw_metadata\":{\"source\":\"manual-demo\"}}"
```

Seed demo data directly in Postgres:

```powershell
docker exec -i ai_gateway_postgres psql -U postgres -d ai_gateway < seeds/demo_inference_data.sql
```

## Architecture Notes

The API stores conversations and messages in Postgres. The inference endpoint uses a short sliding context window from recent messages, calls the provider wrapper, parses text and token usage, then writes an inference log. The ingestion endpoint accepts log payloads from an SDK or middleware path and stores normalized metadata plus raw provider metadata in JSONB.

Redis is used for per-user rate limiting. JWT auth protects application routes. Provider code is behind a small interface. OpenRouter is the default provider for free-model testing, and Anthropic remains available by setting `LLM_PROVIDER=anthropic`.

## Schema Decisions

`conversations` owns conversation lifecycle state. `messages` stores ordered chat history and cascades when a conversation is deleted. `inference_logs` stores provider/model/status, latency, token usage, previews, error text, timestamps, and `raw_metadata` JSONB for provider-specific details.

The log table references conversations and messages with `ON DELETE SET NULL` so analytics history can survive data cleanup.

## Tradeoffs

The inference endpoint currently buffers non-streaming provider responses so it can persist assistant text and token usage in one transaction-like flow. Streaming support exists at the provider/proxy level but is not yet integrated with message persistence. The current context strategy is simple last-N messages instead of summarization or token-aware trimming.

## Improvements With More Time

Add a frontend chat UI, stream persisted responses, add more provider-specific metadata normalization, add dashboards for latency/throughput/errors, add PII redaction before storage, add retries/dead-letter handling for ingestion, and add integration tests with a mocked provider.
