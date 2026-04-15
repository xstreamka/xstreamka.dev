FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -ldflags="-s -w" -o /pay-service ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /pay-service .
COPY internal/templates ./internal/templates
COPY static ./static

ENV TZ=Europe/Moscow
CMD ["./pay-service"]
