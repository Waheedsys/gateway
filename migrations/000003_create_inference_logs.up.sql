CREATE TABLE inference_logs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id     UUID REFERENCES conversations(id) ON DELETE SET NULL,
    message_id          UUID REFERENCES messages(id) ON DELETE SET NULL,
    provider            VARCHAR(50)  NOT NULL,          -- 'anthropic', 'openai', etc
    model               VARCHAR(100) NOT NULL,
    status              VARCHAR(20)  NOT NULL,          -- 'success' | 'error'
    latency_ms          INT,
    prompt_tokens       INT,
    completion_tokens   INT,
    total_tokens        INT,
    input_preview       TEXT,                           -- first 200 chars
    output_preview      TEXT,
    error_message       TEXT,
    raw_metadata        JSONB,                          -- full provider response meta
    requested_at        TIMESTAMPTZ NOT NULL,
    responded_at        TIMESTAMPTZ
);

CREATE INDEX idx_inference_logs_conversation_id ON inference_logs(conversation_id);
CREATE INDEX idx_inference_logs_provider       ON inference_logs(provider);
CREATE INDEX idx_inference_logs_requested_at   ON inference_logs(requested_at DESC);