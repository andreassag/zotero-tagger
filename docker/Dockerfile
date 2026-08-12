# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /zotero-tagger ./cmd/zotero-tagger

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache poppler-utils ca-certificates

COPY --from=builder /zotero-tagger /usr/local/bin/zotero-tagger
COPY config/config.toml /etc/zotero-tagger/config.toml

ENTRYPOINT ["zotero-tagger"]
CMD ["tag", "--config", "/etc/zotero-tagger/config.toml"]
