package main

import (
    "fmt"
    "os"
    "time"
    "github.com/golang-jwt/jwt/v5"
    "github.com/Waheedsys/ai-gateway/internal/auth"
)

func main() {
    claims := &auth.Claims{
        UserID: "user-123",
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
            IssuedAt:  jwt.NewNumericDate(time.Now()),
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    signed, err := token.SignedString([]byte(os.Getenv("JWT_SECRET")))
    if err != nil {
        panic(err)
    }
    fmt.Println(signed)
}