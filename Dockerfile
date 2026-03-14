# ── Build ─────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src

RUN apk add --no-cache git

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" \
    -o vless-aggregator \
    ./cmd/server

# ── Runtime ───────────────────────────────────────────
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /src/vless-aggregator .
COPY config.json .

EXPOSE 8080

ENTRYPOINT ["./vless-aggregator"]
