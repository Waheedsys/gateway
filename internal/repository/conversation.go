package repository

import (
    "context"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/Waheedsys/ai-gateway/internal/models"
)

type Conversation struct {
    db *pgxpool.Pool
}

func NewConversationRepo(db *pgxpool.Pool) *Conversation {
    return &Conversation{db: db}
}

// CREATE
func (r *Conversation) Create(ctx context.Context, title string) (*models.Conversation, error) {
    var c models.Conversation
    err := r.db.QueryRow(ctx, `
        INSERT INTO conversations (title)
        VALUES ($1)
        RETURNING id, title, status, created_at, updated_at
    `, title).Scan(&c.ID, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt)
    return &c, err
}

// GET by ID
func (r *Conversation) GetByID(ctx context.Context, id string) (*models.Conversation, error) {
    var c models.Conversation
    err := r.db.QueryRow(ctx, `
        SELECT id, title, status, created_at, updated_at
        FROM conversations WHERE id = $1
    `, id).Scan(&c.ID, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt)
    return &c, err
}

// LIST all (for sidebar)
func (r *Conversation) List(ctx context.Context) ([]models.Conversation, error) {
    rows, err := r.db.Query(ctx, `
        SELECT id, title, status, created_at, updated_at
        FROM conversations
        ORDER BY updated_at DESC
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var convs []models.Conversation
    for rows.Next() {
        var c models.Conversation
        if err := rows.Scan(&c.ID, &c.Title, &c.Status, &c.CreatedAt, &c.UpdatedAt); err != nil {
            return nil, err
        }
        convs = append(convs, c)
    }
    return convs, rows.Err()
}

// CANCEL (soft status update — not hard delete)
func (r *Conversation) Cancel(ctx context.Context, id string) error {
    _, err := r.db.Exec(ctx, `
        UPDATE conversations
        SET status = 'cancelled', updated_at = $1
        WHERE id = $2
    `, time.Now(), id)
    return err
}

// UPDATE title
func (r *Conversation) UpdateTitle(ctx context.Context, id, title string) error {
    _, err := r.db.Exec(ctx, `
        UPDATE conversations SET title = $1, updated_at = $2 WHERE id = $3
    `, title, time.Now(), id)
    return err
}

// DELETE (hard delete — cascades to messages)
func (r *Conversation) Delete(ctx context.Context, id string) error {
    _, err := r.db.Exec(ctx, `DELETE FROM conversations WHERE id = $1`, id)
    return err
}