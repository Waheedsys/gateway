# AI Gateway

A high-performance, lightweight LLM gateway for multi-turn conversations, log ingestion, and analytics. Built in Go, it features native Server-Sent Events (SSE) streaming, a non-blocking event-driven architecture, Redis-based rate limiting, and robust containerization.

---

## Architecture Overview

The gateway is built around a decoupled, resilient architecture where client responsiveness is prioritized.

```
                  ┌────────────────────────────────────────────────────────┐
                  │                    Client Request                      │
                  └──────────────────────────┬─────────────────────────────┘
                                             │ POST /conversations/{id}/infer
                                             ▼
                                ┌─────────────────────────┐
                                │   Inference Endpoint    │
                                └──────┬────────────┬─────┘
                                       │            │
             If stream=true (SSE path) │            │ If stream=false (Blocking path)
                                       ▼            ▼
             ┌───────────────────────────┐        ┌───────────────────────────┐
             │ Immediate SSE Connection  │        │   Wait for LLM response   │
             │ (Flushes start/text/done) │        │   and return full JSON    │
             └─────────────┬─────────────┘        └─────────────┬─────────────┘
                           │                                    │
                           │     Publish event asynchronously   │
                           └─────────────┐        ┌─────────────┘
                                         │        │
                                         ▼        ▼
                               ┌─────────────────────────┐
                               │  In-Process Event Bus   │
                               │  (Go Buffered Channel)  │
                               └────────────┬────────────┘
                                            │
                                            │ Dispatched to
                                            ▼
                               ┌─────────────────────────┐
                               │   Database Subscriber   │
                               │   (Async DB Write)      │
                               └────────────┬────────────┘
                                            │
                                            ▼
                               ┌─────────────────────────┐
                               │      PostgreSQL DB      │
                               │    (inference_logs)     │
                               └─────────────────────────┘
```

### Key Technical Achievements

1. **Native Server-Sent Events (SSE) Streaming**: Supports real-time client interaction by proxying, parsing, and streaming chunks back to the client immediately using `text/event-stream` with chunk-by-chunk HTTP flushing.
2. **Non-Blocking Event-Driven Persistence**: Eliminates database query latency from the client's request path. HTTP handlers drop event payloads onto a buffered channel in the `events.Bus`. A background goroutine consumes these events to save logs asynchronously.
3. **Defense-in-Depth Concurrency**: Utilizes non-blocking select-default channel publishers so that if the queue becomes completely filled (e.g. during an extreme spike), it falls back to dropping log events rather than freezing incoming client requests. A custom panic recovery defer statement ensures that sub-routine failures never crash the parent server process.
4. **Clean Provider Interfaces**: Highly modular LLM provider abstractions (`providers.Provider` interface) supporting custom token calculation, metadata mapping, and raw response payloads (implemented for **OpenRouter** and **Anthropic**).

---

## Setup & Deployment

The repository comes fully containerized and configured for local development or production-like environments.

### Option A: Running with Docker (Recommended)

To run the entire ecosystem (Go API + Postgres + Redis) with a single command:

1. **Verify your `.env` configuration** or copy it:
   ```env
   DATABASE_URL=postgres://postgres:postgres@postgres:5432/ai_gateway?sslmode=disable
   REDIS_ADDR=redis:6379
   MIGRATIONS_PATH=./migrations
   JWT_SECRET=supersecretjwtkey123!
   LLM_PROVIDER=openrouter
   OPENROUTER_API_KEY=your_key_here
   ```

2. **Build and spin up the Docker stack**:
   ```bash
   docker compose up -d --build
   ```

3. **Verify running containers and health checks**:
   ```bash
   docker compose ps
   ```
   Postgres and Redis carry automated health checks (`pg_isready` and `redis-cli ping` respectively). The gateway will wait to boot until they are reported healthy.

### Option B: Running Locally

If you prefer to run the database & cache inside containers but run the Go process locally:

1. **Launch dependencies**:
   ```bash
   docker compose up -d postgres redis
   ```

2. **Configure `.env` for local access**:
   ```env
   DATABASE_URL=postgres://postgres:postgres@localhost:5433/ai_gateway?sslmode=disable
   REDIS_ADDR=localhost:6379
   MIGRATIONS_PATH=./migrations
   JWT_SECRET=supersecretjwtkey123!
   LLM_PROVIDER=openrouter
   OPENROUTER_API_KEY=your-openrouter-key
   ```

3. **Run the API**:
   ```bash
   go run ./cmd/gateway
   ```
   *Note: Database schema migrations run automatically at startup!*

---

## Authentication

Protected endpoints require a bearer JWT signed with the defined `JWT_SECRET`. 

Generate a token for local testing by running:
```bash
go run ./cmd/gentoken
```

Send the resulting token in the request header:
```text
Authorization: Bearer <token>
```

---

## Core API Endpoints

| Method | Path | Auth Required | Purpose |
| --- | --- | --- | --- |
| `GET` | `/health` | No | Health check |
| `POST` | `/conversations` | Yes | Create a new conversation |
| `GET` | `/conversations` | Yes | List all conversations |
| `GET` | `/conversations/{id}` | Yes | Load conversation state |
| `POST` | `/conversations/{id}/cancel`| Yes | Mark conversation as cancelled (blocks further inference) |
| `GET` | `/conversations/{id}/messages`| Yes | List all chat messages in a conversation |
| `POST` | `/conversations/{id}/infer` | Yes | Store user message, invoke LLM, stream back or block-return assistant reply, publish log event |
| `GET` | `/conversations/{id}/inference-logs` | Yes | List logged inference cycles for this conversation |
| `POST` | `/ingest/inference-logs` | Yes | Ingest analytics logs from external SDKs/wrappers |
| `GET` | `/inference/stats` | Yes | Get historical summary metrics (avg latency, token count, errors) |

---

## Features & Deep Dives

### 1. Server-Sent Events (SSE) Streaming

When invoking the inference endpoint `/conversations/{id}/infer` with `stream: true`, the gateway returns a standard EventSource-compatible chunked stream. 

**Response Event Stream Flow:**
- **`event: start`**: Emitted immediately with the created `user_message` object.
- **`event: text`**: Emitted repeatedly as characters stream back from the LLM provider, providing low-latency interaction.
- **`event: done`**: Emitted when the stream terminates safely, providing the fully persisted `assistant_message` database object.
- **`event: error`**: Emitted in the event of an upstream provider drop or database writing failure.

#### Sample Streaming Request
```bash
curl.exe -X POST http://localhost:8080/conversations/YOUR_CONVERSATION_ID/infer \
  -H "Authorization: Bearer YOUR_JWT_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"content\":\"Explain quantum computing in one sentence.\",\"stream\":true}"
```

#### Sample Streaming Response
```text
data: {"event":"start","user_message":{"id":"...","conversation_id":"...","role":"user","content":"Explain quantum...","created_at":"..."}}

data: {"event":"text","text":"Quantum"}

data: {"event":"text","text":" computing"}

...

data: {"event":"done","assistant_message":{"id":"...","conversation_id":"...","role":"assistant","content":"Quantum computing uses subatomic particles to process complex math at super speeds.","created_at":"..."}}
```

---

### 2. Go Channel-Based Event Bus

To keep inference response times as short as possible, logging is decoupled from the HTTP response thread. 

- **Buffered Go Channel**: The event bus maintains a buffer of size `100` (`events.NewBus(100)`).
- **Graceful Backpressure Management**: Publishers use Go's non-blocking `select` write. If the channel fills completely, the message is gracefully dropped, and a warning is logged. This protects application uptime during severe database bottlenecks.
- **Subscriber Protection**: Subscribers run inside dedicated goroutines. A deferred `recover()` block wraps the subscriber execution context to prevent a sudden runtime panic from taking down the web server.

---

### 3. Redis Rate Limiting

The application applies a custom Token Bucket middleware utilizing Redis. Each client API key has a rate limit applied dynamically:
- Prevents DDoS attacks and LLM provider credit exhaustion.
- Features automatic sliding TTLs and token replenishing.

---

### 4. Database Schema & Persistence

The relational database is configured to survive data cleanup safely:
- **Conversations**: Manages state (e.g. `cancelled` to restrict further chat operations).
- **Messages**: Holds conversation histories. Uses `ON DELETE CASCADE` to clean up messages when their parent conversation is removed.
- **Inference Logs**: Keeps detailed analytics tracking latency, status (`success` / `error`), model parameters, and raw vendor metadata (`JSONB`). Connects via `ON DELETE SET NULL` to keep valuable metrics even if conversations/messages are pruned.

---

## Containerization Architecture

The project's `dockerfile` follows modern container best practices for minimal image sizes and enhanced security:

```
  Stage 1: Statically Build Binary                  Stage 2: Minimal Distroless Alpine
┌─────────────────────────────────┐               ┌──────────────────────────────────┐
│  FROM golang:1.26-alpine        │               │  FROM alpine:3.20                │
│                                 │               │                                  │
│  - Install git                  │               │  - Install ca-certificates      │
│  - Cache go.mod & go.sum        │               │  - Add non-root "gateway" user   │
│  - Copy source code             │               │  - COPY compiled binary          │
│  - Build statically linked app  ├───────────────┼─▶ - COPY migrations/             │
│    (CGO_ENABLED=0 GOOS=linux)   │               │  - USER gateway (security!)      │
│    Size: ~750MB                 │               │  Final size: ~20MB               │
└─────────────────────────────────┘               └──────────────────────────────────┘
```

- **Outbound Outcall Certificates**: Stage 2 installs `ca-certificates` to support HTTPS calls to upstream providers.
- **Rootless Security**: Runs as the custom user `gateway` so the container process has zero access to default system processes.
- **Build Caching**: Dependencies are resolved separately from source changes to keep incremental local container rebuilds under 2 seconds.

---

## Future Roadmap & Improvements

- **Global Metrics Dashboard**: A visualization suite to map gateway analytics (error spikes, average token costs, and average provider response latency).
- **PII Redaction Engine**: Advanced regex and Named Entity Recognition (NER) filters to scrub credit cards, phone numbers, and keys before persisting logs to `inference_logs`.
- **Ingestion Failover Queues**: Dead-Letter Queues (DLQ) or Apache Kafka support for bulk `/ingest/inference-logs` to support enterprise-grade backpressure.
- **Provider Retry & Failover**: Automatic fallback to alternative models/vendors if an upstream service (e.g., OpenRouter) encounters 5xx error rates.
