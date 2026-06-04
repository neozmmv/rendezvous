FROM golang:1.26.3-alpine AS builder
RUN apk add --no-cache git
WORKDIR /app
COPY . .
RUN VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo dev) && \
    go build -ldflags "-X main.Version=${VERSION}" -o main .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/main .
EXPOSE 8000
CMD ["./main"]