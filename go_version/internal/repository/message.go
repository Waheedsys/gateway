package repository

import (
	"context"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/search"
)

const messagesIndex = "messages"

type MessageRepo struct {
	es *search.Client
}

func NewMessageRepo(es *search.Client) *MessageRepo {
	return &MessageRepo{es: es}
}

func (r *MessageRepo) Create(ctx context.Context, convID string, role models.MessageRole, content string) (*models.Message, error) {
	m := &models.Message{
		ID:             newID(),
		ConversationID: convID,
		Role:           role,
		Content:        content,
		CreatedAt:      time.Now().UTC(),
	}
	return m, r.es.Index(ctx, messagesIndex, m.ID, m)
}

func (r *MessageRepo) ListByConversation(ctx context.Context, convID string) ([]models.Message, error) {
	return r.searchMessages(ctx, map[string]any{
		"size": 500,
		"sort": []any{map[string]any{"created_at": map[string]any{"order": "asc"}}},
		"query": map[string]any{
			"term": map[string]any{"conversation_id": convID},
		},
	})
}

func (r *MessageRepo) GetLastN(ctx context.Context, convID string, n int) ([]models.Message, error) {
	msgs, err := r.searchMessages(ctx, map[string]any{
		"size": n,
		"sort": []any{map[string]any{"created_at": map[string]any{"order": "desc"}}},
		"query": map[string]any{
			"term": map[string]any{"conversation_id": convID},
		},
	})
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (r *MessageRepo) SearchRelevant(ctx context.Context, convID string, query string, n int) ([]models.Message, error) {
	return r.searchMessages(ctx, map[string]any{
		"size": n,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"conversation_id": convID}},
				},
				"must": []any{
					map[string]any{"match": map[string]any{"content": query}},
				},
			},
		},
	})
}

func (r *MessageRepo) searchMessages(ctx context.Context, query any) ([]models.Message, error) {
	var res messageSearchResponse
	if err := r.es.Search(ctx, messagesIndex, query, &res); err != nil {
		return nil, err
	}
	msgs := make([]models.Message, 0, len(res.Hits.Hits))
	for _, hit := range res.Hits.Hits {
		msgs = append(msgs, hit.Source)
	}
	return msgs, nil
}

type messageSearchResponse struct {
	Hits struct {
		Hits []struct {
			Source models.Message `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}
