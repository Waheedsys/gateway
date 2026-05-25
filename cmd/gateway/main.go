package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Waheedsys/ai-gateway/internal/auth"
	"github.com/Waheedsys/ai-gateway/internal/db"
	"github.com/Waheedsys/ai-gateway/internal/events"
	"github.com/Waheedsys/ai-gateway/internal/handlers"
	"github.com/Waheedsys/ai-gateway/internal/models"
	"github.com/Waheedsys/ai-gateway/internal/providers"
	"github.com/Waheedsys/ai-gateway/internal/ratelimiter"
	"github.com/Waheedsys/ai-gateway/internal/repository"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 2. Load environment variables
	loadEnv()

	// 3. Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_ADDR"), // "localhost:6379"
	})
	defer rdb.Close()

	// Postgres
	pool, err := db.Connect()
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer pool.Close()
	if err := db.ApplyMigrations(context.Background(), pool); err != nil {
		log.Fatalf("failed to apply database migrations: %v", err)
	}

	conversationRepo := repository.NewConversationRepo(pool)
	messageRepo := repository.NewMessageRepo(pool)
	inferenceLogRepo := repository.NewInferenceLogRepo(pool)
	provider := newProviderFromEnv()

	// 1. Initialize the Event Bus ( buffered channel, background consumer)
	bus := events.NewBus(100)
	defer bus.Close()

	conversationHandler := handlers.NewConversationHandler(conversationRepo, messageRepo, inferenceLogRepo, provider, bus, rdb)
	ingestionHandler := handlers.NewIngestionHandler(inferenceLogRepo)

	bus.Subscribe(func(e events.Event) {
		ctx := context.Background() // handler ctx is already done by now

		switch e.Type {

		case events.EventInferenceCompleted:
			p := e.Payload.(events.InferenceCompletedPayload)
			respondedAt := p.RespondedAt
			_, err := inferenceLogRepo.Create(ctx, &models.InferenceLog{
				ConversationID:   p.ConversationID,
				MessageID:        p.UserMessageID,
				Provider:         p.Provider,
				Model:            p.Model,
				Status:           "success",
				LatencyMs:        p.LatencyMs,
				InputPreview:     p.InputPreview,
				OutputPreview:    p.OutputPreview,
				PromptTokens:     p.PromptTokens,
				CompletionTokens: p.CompletionTokens,
				TotalTokens:      p.TotalTokens,
				RawMetadata:      p.RawMetadata,
				RequestedAt:      p.RequestedAt,
				RespondedAt:      &respondedAt,
			})
			fmt.Println("-------", err)
			if err != nil {
				log.Printf("[events] failed to save inference log: %v", err)
			}

		case events.EventInferenceFailed:
			p := e.Payload.(events.InferenceFailedPayload)
			respondedAt := p.RespondedAt
			_, err := inferenceLogRepo.Create(ctx, &models.InferenceLog{
				ConversationID: p.ConversationID,
				MessageID:      p.UserMessageID,
				Provider:       p.Provider,
				Model:          p.Model,
				Status:         "error",
				LatencyMs:      p.LatencyMs,
				InputPreview:   p.InputPreview,
				ErrorMessage:   p.ErrorMessage,
				RawMetadata:    p.RawMetadata,
				RequestedAt:    p.RequestedAt,
				RespondedAt:    &respondedAt,
			})
			fmt.Println("-------", err)
			if err != nil {
				log.Printf("[events] failed to save failed inference log: %v", err)
			}
		}
	})

	//chi router
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	//health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Use(ratelimiter.Middleware(rdb))
		conversationHandler.Routes(r)
		ingestionHandler.Routes(r)
	})

	log.Println("gateway listening on :8080")

	http.ListenAndServe(":8080", r)
}

func loadEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		envPath := filepath.Join(dir, ".env")
		values, err := godotenv.Read(envPath)
		if err == nil {
			for key, value := range values {
				current, exists := os.LookupEnv(key)
				if !exists || strings.TrimSpace(current) == "" {
					_ = os.Setenv(key, value)
				}
			}
			return
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
	}
}

func newProviderFromEnv() providers.Provider {
	switch strings.ToLower(os.Getenv("LLM_PROVIDER")) {
	case "anthropic":
		return providers.NewAnthropic(os.Getenv("ANTHROPIC_API_KEY"))
	case "openrouter", "":
		return providers.NewOpenRouter(os.Getenv("OPENROUTER_API_KEY"))
	default:
		log.Printf("unknown LLM_PROVIDER=%q, falling back to openrouter", os.Getenv("LLM_PROVIDER"))
		return providers.NewOpenRouter(os.Getenv("OPENROUTER_API_KEY"))
	}
}
