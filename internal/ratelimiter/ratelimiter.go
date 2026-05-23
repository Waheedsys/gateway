package ratelimiter

import (
    "context"
    "net/http"

    "github.com/Waheedsys/ai-gateway/internal/auth"
    "github.com/redis/go-redis/v9"
)

const luaScript = `
local tokens = redis.call('GET', KEYS[1])
if tokens == false then
    redis.call('SET', KEYS[1], tonumber(ARGV[1]) - 1)
    redis.call('EXPIRE', KEYS[1], ARGV[2])
    return 1
end
if tonumber(tokens) >= 1 then
    redis.call('DECR', KEYS[1])
    redis.call('EXPIRE', KEYS[1], ARGV[2])
    return 1
end
return 0
`

var rateLimitScript = redis.NewScript(luaScript)

func Middleware(rdb *redis.Client) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            userID, ok := r.Context().Value(auth.UserKey).(string)
            if !ok {
                http.Error(w, "Unauthorized", http.StatusUnauthorized)
                return
            }

            allowed, err := rateLimitScript.Run(
                context.Background(),
                rdb,
                []string{"rate:" + userID},
                10, // max tokens
                60, // window seconds
            ).Int()

            if err != nil || allowed == 0 {
                http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}