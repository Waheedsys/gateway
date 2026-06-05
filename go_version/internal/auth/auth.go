package auth

import (
	"context"
	"net/http"
	"github.com/golang-jwt/jwt/v5"
	"strings"
	"os"
)
type Claims struct {
    UserID string `json:"user_id"`
    jwt.RegisteredClaims          // has Expiry, IssuedAt, etc. built in
}
type contextKey string

const UserKey contextKey = "user_id"

func Validate(authHeader string) (*Claims, error) {
    // strip "Bearer " prefix
    tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

    claims := &Claims{}
    _, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
        return []byte(os.Getenv("JWT_SECRET")), nil  // your secret key
    })
    if err != nil {
        return nil, err  // expired, wrong signature, malformed, etc.
    }
    return claims, nil
}

func Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("Authorization")
        claims, err := Validate(token)
        if err != nil { http.Error(w, "Unauthorized", 401); return }
        ctx := context.WithValue(r.Context(), UserKey, claims.UserID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}