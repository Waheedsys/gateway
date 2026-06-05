package repository

import (
	"context"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/search"
)

const inferenceLogsIndex = "inference_logs"

type InferenceLog struct {
	es *search.Client
}

func NewInferenceLogRepo(es *search.Client) *InferenceLog {
	return &InferenceLog{es: es}
}

func (r *InferenceLog) Create(ctx context.Context, log *models.InferenceLog) (*models.InferenceLog, error) {
	created := *log
	if created.ID == "" {
		created.ID = newID()
	}
	if created.RequestedAt.IsZero() {
		created.RequestedAt = time.Now().UTC()
	}
	if created.RawMetadata == nil {
		created.RawMetadata = map[string]any{}
	}
	return &created, r.es.Index(ctx, inferenceLogsIndex, created.ID, &created)
}

func (r *InferenceLog) ListByConversation(ctx context.Context, convID string) ([]models.InferenceLog, error) {
	var res inferenceLogSearchResponse
	err := r.es.Search(ctx, inferenceLogsIndex, map[string]any{
		"size": 500,
		"sort": []any{map[string]any{"requested_at": map[string]any{"order": "desc"}}},
		"query": map[string]any{
			"term": map[string]any{"conversation_id": convID},
		},
	}, &res)
	if err != nil {
		return nil, err
	}

	logs := make([]models.InferenceLog, 0, len(res.Hits.Hits))
	for _, hit := range res.Hits.Hits {
		logs = append(logs, hit.Source)
	}
	return logs, nil
}

func (r *InferenceLog) GetStats(ctx context.Context) (map[string]any, error) {
	var res inferenceLogSearchResponse
	err := r.es.Search(ctx, inferenceLogsIndex, map[string]any{
		"size": 10000,
		"query": map[string]any{
			"range": map[string]any{
				"requested_at": map[string]any{
					"gte": time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
				},
			},
		},
	}, &res)
	if err != nil {
		return nil, err
	}

	var errors, totalLatency, totalTokens int
	for _, hit := range res.Hits.Hits {
		log := hit.Source
		if log.Status == "error" {
			errors++
		}
		totalLatency += log.LatencyMs
		totalTokens += log.TotalTokens
	}

	total := len(res.Hits.Hits)
	avgLatency := 0.0
	avgTokens := 0.0
	if total > 0 {
		avgLatency = float64(totalLatency) / float64(total)
		avgTokens = float64(totalTokens) / float64(total)
	}

	return map[string]any{
		"total_requests": total,
		"error_count":    errors,
		"avg_latency_ms": avgLatency,
		"total_tokens":   totalTokens,
		"avg_tokens":     avgTokens,
	}, nil
}

type inferenceLogSearchResponse struct {
	Hits struct {
		Hits []struct {
			Source models.InferenceLog `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}
