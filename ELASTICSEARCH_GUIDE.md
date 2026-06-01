# Elasticsearch Integration Guide

## Overview
This project uses Elasticsearch as the primary data store for:
- **Conversations** - conversation metadata and status
- **Messages** - chat messages within conversations  
- **Inference Logs** - LLM API call telemetry and analytics

---

## 1. Connection Setup

### Environment Configuration
The connection is configured via environment variables:
- `ELASTICSEARCH_URL` - The Elasticsearch server URL (defaults to `http://localhost:9200`)

### Docker Setup
In `docker-compose.yml`:
```yaml
elasticsearch:
  image: docker.elastic.co/elasticsearch/elasticsearch:9.0.0
  environment:
    discovery.type: single-node        # Single node cluster
    xpack.security.enabled: "false"    # No security for development
    ES_JAVA_OPTS: "-Xms512m -Xmx512m" # Memory allocation
  ports:
    - "9200:9200"                      # REST API port
```

Also includes **Kibana** for visual debugging at `http://localhost:5601`

### Connection Flow
In `cmd/gateway/main.go`:
```go
es, err := search.Connect()  // Connects to ELASTICSEARCH_URL
if err != nil {
    log.Fatalf("failed to connect to elasticsearch: %v", err)
}
if err := es.EnsureIndexes(context.Background()); err != nil {
    log.Fatalf("failed to create elasticsearch indexes: %v", err)
}
```

The `Connect()` function:
1. Reads `ELASTICSEARCH_URL` environment variable
2. Creates an HTTP client with 15-second timeout
3. Pings the server to verify connectivity
4. Returns a `*Client` for operations

---

## 2. Index Creation

### Auto-Index Creation
The `EnsureIndexes()` method automatically creates 3 indexes on startup:

#### A. **conversations** Index
```json
{
  "mappings": {
    "properties": {
      "id":         { "type": "keyword" },
      "title":      { "type": "text", "fields": { "keyword": { "type": "keyword" } } },
      "status":     { "type": "keyword" },
      "created_at": { "type": "date" },
      "updated_at": { "type": "date" }
    }
  }
}
```

**Field Types Explained:**
- `keyword` - exact match, not analyzed, used for filtering/sorting
- `text` - analyzed for full-text search
- `date` - ISO 8601 timestamps

#### B. **messages** Index
```json
{
  "mappings": {
    "properties": {
      "id":              { "type": "keyword" },
      "conversation_id": { "type": "keyword" },
      "role":            { "type": "keyword" },
      "content":         { "type": "text" },
      "created_at":      { "type": "date" }
    }
  }
}
```

#### C. **inference_logs** Index
```json
{
  "mappings": {
    "properties": {
      "id":                { "type": "keyword" },
      "conversation_id":   { "type": "keyword" },
      "message_id":        { "type": "keyword" },
      "provider":          { "type": "keyword" },
      "model":             { "type": "keyword" },
      "status":            { "type": "keyword" },
      "latency_ms":        { "type": "integer" },
      "prompt_tokens":     { "type": "integer" },
      "completion_tokens": { "type": "integer" },
      "total_tokens":      { "type": "integer" },
      "input_preview":     { "type": "text" },
      "output_preview":    { "type": "text" },
      "error_message":     { "type": "text" },
      "raw_metadata":      { "type": "object", "enabled": false },
      "requested_at":      { "type": "date" },
      "responded_at":      { "type": "date" }
    }
  }
}
```

### Index Creation Logic
In `internal/search/client.go`:
```go
func (c *Client) EnsureIndexes(ctx context.Context) error {
    indexes := map[string]map[string]any{ /* index configs */ }
    
    for index, body := range indexes {
        exists, err := c.indexExists(ctx, index)  // Check if exists
        if err != nil {
            return err
        }
        if exists {
            continue  // Skip if already exists
        }
        // Create with PUT /<index> request
        if err := c.doJSON(ctx, http.MethodPut, "/"+index, body, nil); err != nil {
            return err
        }
    }
    return nil
}
```

**Key Points:**
- Idempotent: only creates if index doesn't exist
- All indexes are created on application startup
- Mappings are immutable after index creation (index-level mappings can't be changed)

---

## 3. Core Operations

### The Search Client
`internal/search/client.go` provides low-level HTTP operations:

```go
type Client struct {
    baseURL string           // Elasticsearch server URL
    http    *http.Client     // HTTP client with 15s timeout
}
```

#### Key Methods:

**Index (Create/Update Document)**
```go
func (c *Client) Index(ctx context.Context, index, id string, doc any) error
```
- `PUT /<index>/_doc/<id>?refresh=true`
- Upserts document (creates or overwrites)
- `refresh=true` ensures document appears in searches immediately

**Get (Fetch Single Document)**
```go
func (c *Client) Get(ctx context.Context, index, id string, dest any) error
```
- `GET /<index>/_doc/<id>`
- Returns 404 error if document not found
- Unmarshals `_source` field into destination struct

**Update (Partial Update)**
```go
func (c *Client) Update(ctx context.Context, index, id string, partial any) error
```
- `POST /<index>/_update/<id>?refresh=true`
- Only updates fields in `{"doc": {...}}` payload
- Merges with existing document

**Delete**
```go
func (c *Client) Delete(ctx context.Context, index, id string) error
```
- `DELETE /<index>/_doc/<id>?refresh=true`

**Search (Query Documents)**
```go
func (c *Client) Search(ctx context.Context, index string, query any, dest any) error
```
- `POST /<index>/_search`
- Accepts Elasticsearch query DSL
- Unmarshals response into destination struct

---

## 4. Repository Layer (Data Access)

Three repositories wrap the search client for domain models:

### A. Conversation Repository
`internal/repository/conversation.go`

```go
type Conversation struct {
    es *search.Client
}
```

**Operations:**
- `Create(ctx, title)` - Creates new conversation
- `GetByID(ctx, id)` - Fetches by ID
- `List(ctx)` - Lists all conversations, sorted by updated_at descending
- `UpdateTitle(ctx, id, title)` - Updates title
- `Cancel(ctx, id)` - Sets status to cancelled
- `Delete(ctx, id)` - Deletes conversation

**Example Query (List):**
```go
"query": { "match_all": {} }  // Get all documents
"size": 100                    // Limit to 100
"sort": [{ "updated_at": { "order": "desc" } }]  // Sort by newest first
```

### B. Message Repository
`internal/repository/message.go`

```go
type MessageRepo struct {
    es *search.Client
}
```

**Operations:**
- `Create(ctx, convID, role, content)` - Creates message in conversation
- `ListByConversation(ctx, convID)` - Gets all messages for a conversation
- `GetLastN(ctx, convID, n)` - Gets last N messages (useful for LLM context)
- `SearchRelevant(ctx, convID, query, n)` - Full-text search within conversation

**Example Query (ListByConversation):**
```go
"query": {
    "term": { "conversation_id": convID }  // Filter by conversation
}
"sort": [{ "created_at": { "order": "asc" } }]  // Chronological order
```

**Example Query (SearchRelevant):**
Uses full-text search on message content across conversation, returns top N matches.

### C. Inference Log Repository
`internal/repository/inference_log.go`

```go
type InferenceLog struct {
    es *search.Client
}
```

**Operations:**
- `Create(ctx, log)` - Creates inference log record
- `ListByConversation(ctx, convID)` - Gets all LLM calls for a conversation
- `GetStats(ctx)` - Aggregates statistics (token usage, latency, etc.)

**Stored Data:**
- Provider/model used
- Token counts (prompt, completion, total)
- Latency in milliseconds
- Request/response timestamps
- Error messages
- API response metadata

---

## 5. Query Patterns & DSL

### Elasticsearch Query DSL Used

**1. Match All (List All Documents)**
```json
{
  "query": {
    "match_all": {}
  },
  "size": 100
}
```

**2. Term Query (Exact Field Match - keyword fields)**
```json
{
  "query": {
    "term": {
      "conversation_id": "conv_12345"
    }
  }
}
```
- Used for filtering by ID or keyword fields
- No text analysis performed

**3. Full-Text Search (Text Fields)**
```json
{
  "query": {
    "multi_match": {
      "query": "search term",
      "fields": ["content", "title"]
    }
  }
}
```
- Searches across multiple text fields
- Performs text analysis (stemming, tokenization)

**4. Range Query (Dates, Numbers)**
```json
{
  "query": {
    "range": {
      "requested_at": {
        "gte": "2025-01-01T00:00:00Z",
        "lte": "2025-12-31T23:59:59Z"
      }
    }
  }
}
```

**5. Bool Query (Complex Filters)**
```json
{
  "query": {
    "bool": {
      "must": [
        { "term": { "conversation_id": "conv_123" } }
      ],
      "filter": [
        { "range": { "created_at": { "gte": "2025-01-01" } } }
      ]
    }
  }
}
```

### Sorting
```json
{
  "sort": [
    { "created_at": { "order": "desc" } }
  ]
}
```
- Fields must be `keyword` or `date` type for sorting
- `order`: "asc" (ascending) or "desc" (descending)

---

## 6. Data Flow in Application

### On Conversation Creation
```
Handler receives request
  ↓
ConversationRepo.Create(ctx, title)
  ↓
search.Client.Index()
  ↓
PUT /conversations/_doc/<id>
  ↓
Document stored in Elasticsearch
```

### On Message Creation
```
Handler receives message
  ↓
MessageRepo.Create(ctx, convID, role, content)
  ↓
search.Client.Index()
  ↓
PUT /messages/_doc/<id>
  ↓
Document indexed (full-text search ready)
```

### On Inference Log Creation
```
Event bus publishes InferenceCompleted event
  ↓
Event subscriber calls InferenceLogRepo.Create()
  ↓
search.Client.Index()
  ↓
PUT /inference_logs/_doc/<id>
  ↓
Analytics data available for queries
```

### On Retrieving Conversation History
```
Handler requests conversation messages
  ↓
MessageRepo.ListByConversation(ctx, convID)
  ↓
search.Client.Search()
  ↓
POST /messages/_search
  ↓
Term query filters by conversation_id
  ↓
Results sorted by created_at (ascending)
```

---

## 7. Configuration & Deployment

### Development (Docker)
```bash
docker-compose up
```
- Elasticsearch on `http://localhost:9200`
- Kibana on `http://localhost:5601` (visual debugging)
- No authentication enabled

### Production Considerations
To enable security:
```yaml
elasticsearch:
  environment:
    xpack.security.enabled: "true"
    ELASTIC_PASSWORD: "your-password"
```

Then update `ELASTICSEARCH_URL`:
```
ELASTICSEARCH_URL=http://elastic:password@elasticsearch:9200
```

### Scaling Notes
- **Single node**: Current setup (suitable for development/small deployments)
- **Multi-node**: Change `discovery.type: single-node` to enable cluster mode
- **Sharding**: Add `"settings": {"number_of_shards": 3}` to index creation

---

## 8. Debugging with Kibana

Access at `http://localhost:5601`

**View All Documents in Index:**
```
GET /conversations/_search
{
  "query": { "match_all": {} },
  "size": 100
}
```

**Monitor Index Health:**
```
GET /_cat/indices?v
```

**Delete Index (WARNING - Data loss):**
```
DELETE /conversations
```

---

## 9. Example: Complete Flow

**1. Create Conversation**
```go
conversation, _ := conversationRepo.Create(ctx, "My AI Chat")
// ID: "conv_a1b2c3d4"
```

**2. Add Message**
```go
message, _ := messageRepo.Create(ctx, "conv_a1b2c3d4", "user", "Hello, how are you?")
// ID: "msg_x9y8z7w6"
```

**3. Retrieve History**
```go
messages, _ := messageRepo.ListByConversation(ctx, "conv_a1b2c3d4")
// Returns: [{ id, role, content, created_at }]
```

**4. Search Messages**
```go
results, _ := messageRepo.SearchRelevant(ctx, "conv_a1b2c3d4", "hello", 5)
// Returns top 5 messages matching "hello"
```

**5. Log Inference Call**
```go
log, _ := inferenceLogRepo.Create(ctx, &InferenceLog{
    ConversationID: "conv_a1b2c3d4",
    Provider: "anthropic",
    Model: "claude-3-sonnet",
    Status: "success",
    PromptTokens: 150,
    CompletionTokens: 200,
})
```

---

## 10. Key Takeaways

| Aspect | Details |
|--------|---------|
| **Connection** | HTTP REST client, environment-based URL |
| **Indexes** | 3 auto-created: conversations, messages, inference_logs |
| **Field Types** | keyword (exact), text (searchable), date, integer |
| **Operations** | Index, Get, Update, Delete, Search |
| **Queries** | Term, match_all, range, multi_match, bool |
| **Refresh** | Documents appear in searches immediately (refresh=true) |
| **Idempotency** | Index creation is safe to run multiple times |
| **Debugging** | Kibana UI at :5601 |
| **Security** | Disabled in dev, needs configuration for production |

