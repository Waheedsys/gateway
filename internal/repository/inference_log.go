package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InferenceLog struct {
	db *pgxpool.Pool
}

func NewInferenceLogRepo(db *pgxpool.Pool) *InferenceLog {
	return &InferenceLog{db: db}
}

// INSERT — called by ingestion endpoint
func (r *InferenceLog) Create(ctx context.Context, log *models.InferenceLog) (*models.InferenceLog, error) {
	meta, _ := json.Marshal(log.RawMetadata)
	var createdID string
	var createdRequestedAt time.Time
	err := r.db.QueryRow(ctx, `
        INSERT INTO inference_logs (
            conversation_id, message_id, provider, model, status,
            latency_ms, prompt_tokens, completion_tokens, total_tokens,
            input_preview, output_preview, error_message,
            raw_metadata, requested_at, responded_at
        ) VALUES (
            $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
        )
        RETURNING id, requested_at
    `,
		nullableString(log.ConversationID), nullableString(log.MessageID), log.Provider, log.Model, log.Status,
		log.LatencyMs, log.PromptTokens, log.CompletionTokens, log.TotalTokens,
		log.InputPreview, log.OutputPreview, log.ErrorMessage,
		meta, log.RequestedAt, log.RespondedAt,
	).Scan(&createdID, &createdRequestedAt)
	if err != nil {
		return nil, err
	}

	created := *log
	created.ID = createdID
	created.RequestedAt = createdRequestedAt
	return &created, nil
}

// LIST logs for a conversation
func (r *InferenceLog) ListByConversation(ctx context.Context, convID string) ([]models.InferenceLog, error) {
	rows, err := r.db.Query(ctx, `
        SELECT id, conversation_id, message_id, provider, model, status,
               latency_ms, prompt_tokens, completion_tokens, total_tokens,
               input_preview, output_preview, error_message, requested_at, responded_at
        FROM inference_logs
        WHERE conversation_id = $1
        ORDER BY requested_at DESC
    `, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.InferenceLog
	for rows.Next() {
		var l models.InferenceLog
		rows.Scan(&l.ID, &l.ConversationID, &l.MessageID, &l.Provider, &l.Model,
			&l.Status, &l.LatencyMs, &l.PromptTokens, &l.CompletionTokens,
			&l.TotalTokens, &l.InputPreview, &l.OutputPreview, &l.ErrorMessage,
			&l.RequestedAt, &l.RespondedAt)
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// DASHBOARD stats — latency, tokens, error rate
func (r *InferenceLog) GetStats(ctx context.Context) (map[string]any, error) {
	var stats map[string]any
	row := r.db.QueryRow(ctx, `
        SELECT
            COUNT(*)                                        AS total_requests,
            COUNT(*) FILTER (WHERE status = 'error')       AS error_count,
            COALESCE(AVG(latency_ms), 0)                    AS avg_latency_ms,
            COALESCE(SUM(total_tokens), 0)                  AS total_tokens,
            COALESCE(AVG(total_tokens), 0)                  AS avg_tokens_per_req
        FROM inference_logs
        WHERE requested_at > NOW() - INTERVAL '24 hours'
    `)
	var total, errors int
	var avgLatency, totalTokens, avgTokens float64
	if err := row.Scan(&total, &errors, &avgLatency, &totalTokens, &avgTokens); err != nil {
		return nil, err
	}
	stats = map[string]any{
		"total_requests": total,
		"error_count":    errors,
		"avg_latency_ms": avgLatency,
		"total_tokens":   totalTokens,
		"avg_tokens":     avgTokens,
	}
	return stats, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
