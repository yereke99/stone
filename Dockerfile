FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/stone ./cmd

FROM alpine:3.22

RUN adduser -D -g "" appuser

WORKDIR /app
COPY --from=builder /bin/stone /app/stone
COPY --from=builder /app/video /app/video

USER appuser

ENTRYPOINT ["/app/stone"]
