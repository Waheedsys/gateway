# AI Gateway: Architectural Design & Implementation Notes

The AI Gateway is a high-performance, resilient, and lightweight service built in **Go** designed to mediate and orchestrate client interactions with various Large Language Model (LLM) backends. It integrates real-time Server-Sent Events (SSE) streaming, an asynchronous in-process event bus, rate limiting, and structured persistence for logging and analytics.

This document details the architectural decisions, component mappings, data flows, and concurrency protections that ensure high reliability, near-zero database latency on the request path, and modular extensibility.

---

## 1. Executive System Topology

The AI Gateway is designed with a decoupled architecture that isolates client-facing HTTP operations from slow persistent logging tasks.

```
                      ┌────────────────────────────────────────┐
                      │             Client Request             │
                      └───────────────────┬────────────────────┘
                                          │
                                          ▼ [POST /conversations/{id}/infer]
                                 ┌─────────────────┐
                                 │   Chi Router    │
                                 └────────┬────────┘
                                          │
                                          ├─► JWT Auth Middleware (internal/auth)
                                          │
                                          ├─► Redis Rate Limiter (internal/ratelimiter)
                                          │
                                          ▼
                               ┌─────────────────────┐
                               │ Inference Handler   │
                               └──────────┬──────────┘
                                          │
                   ┌──────────────────────┴──────────────────────┐
                   │                                             │
      If stream = true (SSE Path)                  If stream = false (Blocking Path)
                   │                                             │
                   ▼                                             ▼
     ┌───────────────────────────┐                 ┌───────────────────────────┐
     │ Flush start payload to SSE│                 │   Send HTTP Post to LLM   │
     │                           │                 │                           │
     │ Read stream chunk-by-chunk│                 │    Wait for Full JSON     │
     │ Flush text chunks directly│                 │                           │
     │                           │                 │  Parse & Return Response  │
     └─────────────┬─────────────┘                 └─────────────┬─────────────┘
                   │                                             │
                   └──────────────────────┬──────────────────────┘
                                          │
                                          │ Asynchronously publish log event
                                          ▼
                             ┌─────────────────────────┐
                             │  events.Bus (Channel)   │
                             └────────────┬────────────┘
                                          │
                                          │ [Consumer Goroutine]
                                          ▼
                             ┌─────────────────────────┐
                             │    DB Event Handler     │
                             └────────────┬────────────┘
                                          │
                                          ▼
                             ┌─────────────────────────┐
                             │  PostgreSQL Database    │
                             └─────────────────────────┘
```

---

## 2. Core Architectural Components

### 2.1 Entrypoint & Routing (`cmd/gateway/main.go`)
- **Lifecycle Management**: Boots the system, connects to dependency instances (PostgreSQL and Redis), and runs schema migrations.
- **Routing Engine**: Leverages `go-chi/chi/v5` for optimized trie-based URL pattern matching.
- **Middleware Pipeline**:
  - `middleware.Logger` and `middleware.Recoverer` provide global request logging and panic handling for safe execution.
  - Custom `auth.Middleware` validates JWT authentication tokens.
  - Custom `ratelimiter.Middleware` applies rate-limiting policies at the API key level.

### 2.2 Security & Access Control (`internal/auth`)
- **JWT Middleware**: Validates standard Bearer JWT tokens from the `Authorization` header.
- **Secret Verification**: Uses a shared cryptographically signed secret (`JWT_SECRET`) loaded dynamically from environment variables.
- **Payload Security**: Grants authenticated access to specific user scopes, ensuring tenant isolation for database queries.

### 2.3 Cache & Token Rate Limiting (`internal/ratelimiter`)
- **Data Store**: Driven by Redis (`go-redis/v9`) for sub-millisecond, centralized latency tracking.
- **Token Bucket Algorithm**: Controls request volumes dynamically, blocking abusive clients or infinite execution loops, thus preventing credit exhaustion on upstream LLM vendors.
- **Sliding Time-To-Live (TTL)**: Refills keys automatically, scaling state space cleanly as client count expands.

### 2.4 Multi-Provider LLM Abstraction (`internal/providers`)
At the core of the service is a decoupling layer between client requests and upstream endpoints.
- **Provider Interface**: Defined in `internal/providers/provider.go`. Any vendor (Anthropic, OpenRouter, OpenAI, etc.) must implement the standard contract:
  ```go
  type Provider interface {
      Complete(ctx context.Context, model, prompt string, stream bool) (*http.Response, error)
      ParseResponse(body io.Reader) (*Completion, error)
      ParseStreamChunk(line string) (*StreamChunk, error)
      Name() string
      DefaultModel() string
  }
  ```
- **Anthropic Provider (`anthropic.go`)**: Prepares messages using the native Anthropic JSON payload scheme and parses incoming SSE events (specifically searching for `content_block_delta` blocks).
- **OpenRouter Provider (`openrouter.go`)**: Packages standard payloads, applies OpenRouter specific headers (like `HTTP-Referer`), and decodes standard SSE chunks containing target text content.

### 2.5 Resilient Asynchronous Event Bus (`internal/events`)
A critical requirement of the gateway is **near-zero latency overhead for analytical persistence**. Storing inference logs directly during the HTTP request thread would add database roundtrip times to client wait times.
- **Buffered Channels**: The `events.Bus` uses a thread-safe, buffered Go channel (`chan Event`).
- **Non-Blocking Publishers**: The handler issues events using a non-blocking `select` write.
  ```go
  select {
  case b.ch <- event: // Success, event stored in memory queue
  default:            // Failover, queue is full. Drop event to maintain HTTP thread uptime
      log.Printf("bus buffer full, dropping event: %s", event.Type)
  }
  ```
- **Self-Healing Background Subscribers**: The event subscriber executes in a separate background goroutine. Crucially, a panic recovery defer block is embedded directly inside the loop wrapper:
  ```go
  defer func() {
      if r := recover(); r != nil {
          log.Printf("[events] subscriber panic recovered: %v", r)
      }
  }()
  ```
  If a single database write fails or panics, the handler recovers instantly, keeping the event bus channel alive and preventing a server-wide crash.

### 2.6 Persistence Layer (`internal/db`, `internal/repository`)
- **Database Engine**: Powered by PostgreSQL using `jackc/pgx/v5` connection pooling for concurrent client safety.
- **Conversations Repository**: Creates and tracks chat groups, supporting cancellation states. If status is set to `cancelled`, subsequent inference runs are blocked instantly.
- **Messages Repository**: Manages multi-turn conversation logs. Relies on structured indices to quickly return the last $N$ messages for conversation prompt context injection.
- **Inference Log Repository**: Persists operational metrics, token counts, request status (`success` or `error`), raw response metadata (`jsonb`), and latency metrics.

---

## 3. Advanced Stream Orchestration

The AI Gateway supports highly efficient real-time Server-Sent Events (SSE) streaming by acting as a streaming proxy between the LLM provider and the client.

```
 Client             Gateway             Upstream LLM
   │                   │                     │
   │─── /infer ───────►│                     │
   │   (stream=true)   │─── /complete ──────►│
   │                   │    (stream=true)    │
   │                   │                     │
   │◄── SSE Headers ───│                     │
   │                   │◄── Chunk 1 ─────────│
   │◄── data: start ───│                     │
   │◄── data: text ────│                     │
   │                   │◄── Chunk 2 ─────────│
   │◄── data: text ────│                     │
   │                   │...                  │
   │                   │◄── [DONE] ──────────│
   │◄── data: done ────│                     │
   │                   │                     │
   ▼                   ▼                     ▼
```

### 3.1 Network Optimization & Flushing
- Direct SSE responses use standard `text/event-stream` formats with strict caching configurations (`Cache-Control: no-cache` and `Connection: keep-alive`).
- Downstream proxies (like Nginx) are prevented from buffering chunks by injecting the `X-Accel-Buffering: no` header.
- The Go writer performs explicit chunk flushing using `http.Flusher`. This pushes data down the socket immediately as soon as a single byte returns from the LLM provider, achieving minimal Time-To-First-Token (TTFT) metrics.

### 3.2 Structured Stream States
The client-facing SSE emitter outputs unified event envelopes in real time:
1. **`event: start`**: Dispatched immediately. Contains the user's persisted message details so the client UI can render it.
2. **`event: text`**: Dispatched repeatedly as raw token strings emerge from the LLM.
3. **`event: done`**: Dispatched once the provider signals completion. Emits the final structured assistant message.
4. **`event: error`**: Emitted if any step breaks. Returns error envelopes gracefully without breaking raw server runtimes.

---

## 4. Database Schema Design

### 4.1 Relationship Architecture
The database schema handles clean cascade options to preserve operational metrics:
- **`conversations`**: Tracks general room status and creation timestamps.
- **`messages`**: Contains actual content and roles (`user`/`assistant`). Configured with `ON DELETE CASCADE` linked to conversations to prevent orphan records.
- **`inference_logs`**: Tracks analytical information. Configured with `ON DELETE SET NULL` on the foreign key relation, which ensures that even if user conversations are deleted, important operational metrics (tokens used, model cost, vendor latency, errors) remain fully intact for business analytics.

---

## 5. Containerization Strategy

The gateway is built with a production-grade Docker design that guarantees a small footprint, speedy builds, and an exceptionally secure runtime.

```
       [ Stage 1: Build Platform ]
  FROM golang:1.26-alpine AS builder
               │
               ├─► Install git & ca-certificates
               ├─► Cache Go module dependencies
               ├─► Compile statically linked binary
               │   (CGO_ENABLED=0 GOOS=linux)
               ▼
      [ Stage 2: Target Image ]
  FROM alpine:3.20
               │
               ├─► Import CA Certificates (SSL/TLS support)
               ├─► Add non-root system user "gateway"
               ├─► Copy compiled binary
               ├─► Set USER to "gateway"
               ▼
   [ Final Secure Image Size: ~20MB ]
```

- **Outbound SSL/TLS Compatibility**: Stage 2 imports `ca-certificates` explicitly, ensuring that outgoing HTTP requests to LLM platforms succeed over HTTPS.
- **Non-Root Isolation**: Operates under the custom system user `gateway` with a UID/GID of 10001. This guarantees that if a remote execution vulnerability ever occurred within the application, the attacker would have zero administrative access inside the underlying host namespace.
- **Zero-Dependency Core**: Compiling with `CGO_ENABLED=0` produces a statically linked self-contained binary, eliminating runtime requirements on native Linux libraries.

---

## 6. Future Scalability Recommendations

As the platform scales to handle millions of monthly inferences, the following enhancements should be implemented:

1. **Dead Letter Queues (DLQ) & Durable Brokers**: Upgrade the in-memory Go `events.Bus` to a message broker like Apache Kafka or RabbitMQ. This ensures that analytical logs survive service restarts and handles bursty workloads gracefully.
2. **PII Redaction Engine**: Integrate a pipeline processor into `events.Bus` subscribers. Using high-speed regex or Named Entity Recognition (NER), automatically redact details like credit cards, passwords, or emails from input/output previews before saving them to `inference_logs`.
3. **Provider Failover Policies**: Implement circuit breakers. If the primary provider (e.g., OpenRouter) encounters consecutive HTTP 5xx errors, automatically route subsequent requests to Anthropic or OpenAI to maintain high service availability.
4. **Token Cache Rate Limiting**: Optimize Redis token replenishment by moving logic into Lua scripts. This eliminates potential race conditions under high concurrency and reduces Redis network roundtrips.
