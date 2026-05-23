package db

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect() (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	// Verify connection
	if err := pool.Ping(context.Background()); err != nil {
		return nil, err
	}

	log.Println("Connected to PostgreSQL successfully")

	return pool, nil
}
