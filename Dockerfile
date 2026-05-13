FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o main .

FROM alpine:3.21
RUN apk add --no-cache curl && adduser -D -h /app appuser
WORKDIR /app
COPY --chown=appuser:appuser --from=builder /app/main ./main
USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
    CMD curl -f http://localhost:8080/livez || exit 1

CMD ["./main"]