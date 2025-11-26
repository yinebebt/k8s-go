FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY main.go .
RUN GO111MODULE=off go build -o main .

FROM alpine:3.21
WORKDIR /app
COPY --from=builder /app/main ./main
EXPOSE 8080
CMD ["./main"]
