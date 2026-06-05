package repository

import (
	"context"
	"time"

	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/search"
)

const conversationsIndex = "conversations"

type Conversation struct {
	es *search.Client
}

func NewConversationRepo(es *search.Client) *Conversation {
	return &Conversation{es: es}
}

func (r *Conversation) Create(ctx context.Context, title string) (*models.Conversation, error) {
	now := time.Now().UTC()
	c := &models.Conversation{
		ID:        newID(),
		Title:     title,
		Status:    models.StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return c, r.es.Index(ctx, conversationsIndex, c.ID, c)
}

func (r *Conversation) GetByID(ctx context.Context, id string) (*models.Conversation, error) {
	var c models.Conversation
	if err := r.es.Get(ctx, conversationsIndex, id, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Conversation) List(ctx context.Context) ([]models.Conversation, error) {
	var res conversationSearchResponse
	err := r.es.Search(ctx, conversationsIndex, map[string]any{
		"size":  100,
		"sort":  []any{map[string]any{"updated_at": map[string]any{"order": "desc"}}},
		"query": map[string]any{"match_all": map[string]any{}},
	}, &res)
	if err != nil {
		return nil, err
	}

	convs := make([]models.Conversation, 0, len(res.Hits.Hits))
	for _, hit := range res.Hits.Hits {
		convs = append(convs, hit.Source)
	}
	return convs, nil
}

func (r *Conversation) Cancel(ctx context.Context, id string) error {
	return r.es.Update(ctx, conversationsIndex, id, map[string]any{
		"status":     models.StatusCancelled,
		"updated_at": time.Now().UTC(),
	})
}

func (r *Conversation) UpdateTitle(ctx context.Context, id, title string) error {
	return r.es.Update(ctx, conversationsIndex, id, map[string]any{
		"title":      title,
		"updated_at": time.Now().UTC(),
	})
}

func (r *Conversation) Delete(ctx context.Context, id string) error {
	return r.es.Delete(ctx, conversationsIndex, id)
}

type conversationSearchResponse struct {
	Hits struct {
		Hits []struct {
			Source models.Conversation `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}
