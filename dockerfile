# â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
# Stage 1 â€“ Build
# â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
FROM golang:1.26-alpine AS builder

# Install git (needed by `go mod download` for private/vcs deps)
RUN apk add --no-cache git

WORKDIR /app

# Cache dependency downloads separately from source compilation
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source tree
COPY . .

# Build a statically-linked binary (no libc dependency in the final image)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /gateway ./cmd/gateway

# â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
# Stage 2 â€“ Final (minimal runtime image)
# â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
FROM alpine:3.20

# ca-certificates â†’ needed for outbound TLS calls to OpenRouter / Anthropic
# tzdata          â†’ optional, keeps time-zone handling consistent
RUN apk add --no-cache ca-certificates tzdata

# Run as a non-root user for security
RUN addgroup -S gateway && adduser -S gateway -G gateway

WORKDIR /app

# Copy compiled binary
COPY --from=builder /gateway ./gateway

# Ensure the binary is executable and owned by the runtime user
RUN chown -R gateway:gateway /app
USER gateway

# The gateway listens on 8080
EXPOSE 8080

ENTRYPOINT ["./gateway"]

