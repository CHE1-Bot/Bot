# syntax=docker/dockerfile:1.7

FROM golang:1.22-alpine AS builder
WORKDIR /src

# Cache modules separately from source for faster rebuilds.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# Static, stripped binary. CGO off so the scratch/distroless runtime works.
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-trimpath
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build -ldflags="-s -w" -o /out/che1-bot .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/che1-bot /app/che1-bot

# Health port (Worker is :8080/:8090; we use :8081 for the bot's probe).
EXPOSE 8081

USER nonroot:nonroot
ENTRYPOINT ["/app/che1-bot"]
