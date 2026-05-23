package repository

import (
    "context"
    "github.com/Waheedsys/ai-gateway/internal/models"
    "github.com/jackc/pgx/v5/pgxpool"
)

type MessageRepo struct {
    db *pgxpool.Pool
}


func NewMessageRepo(db *pgxpool.Pool) *MessageRepo {
    return &MessageRepo{db: db}
}

// INSERT a message
func (r *MessageRepo) Create(ctx context.Context, convID string, role models.MessageRole, content string) (*models.Message, error) {
    var m models.Message
    err := r.db.QueryRow(ctx, `
        INSERT INTO messages (conversation_id, role, content)
        VALUES ($1, $2, $3)
        RETURNING id, conversation_id, role, content, created_at
    `, convID, role, content).Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt)
    return &m, err
}

// GET full history for a conversation (used to resume + for LLM context)
func (r *MessageRepo) ListByConversation(ctx context.Context, convID string) ([]models.Message, error) {
    rows, err := r.db.Query(ctx, `
        SELECT id, conversation_id, role, content, created_at
        FROM messages
        WHERE conversation_id = $1
        ORDER BY created_at ASC
    `, convID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var msgs []models.Message
    for rows.Next() {
        var m models.Message
        if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
            return nil, err
        }
        msgs = append(msgs, m)
    }
    return msgs, rows.Err()
}

// GET last N messages (for sliding context window)
func (r *MessageRepo) GetLastN(ctx context.Context, convID string, n int) ([]models.Message, error) {
    rows, err := r.db.Query(ctx, `
        SELECT id, conversation_id, role, content, created_at
        FROM (
            SELECT * FROM messages
            WHERE conversation_id = $1
            ORDER BY created_at DESC
            LIMIT $2
        ) sub
        ORDER BY created_at ASC
    `, convID, n)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var msgs []models.Message
    for rows.Next() {
        var m models.Message
        rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.CreatedAt)
        msgs = append(msgs, m)
    }
    return msgs, rows.Err()
}