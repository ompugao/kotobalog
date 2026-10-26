# Stage 1: Build Flutter Web
FROM ghcr.io/cirruslabs/flutter:stable AS web-builder
WORKDIR /workspace/app
COPY app/pubspec.* ./
RUN flutter pub get
COPY app/ ./
RUN flutter build web --release

# Stage 2: Build Go Backend
FROM golang:1.24-alpine AS go-builder
WORKDIR /workspace/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /kotobalog-server ./cmd/server

# Stage 3: Final Lightweight Runtime Image for NAS
FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /kotobalog-server /app/kotobalog-server
COPY --from=web-builder /workspace/app/build/web /app/web_dist

ENV PORT=8080
ENV DB_PATH=/app/data/kotobalog.db
ENV AUDIO_DIR=/app/data/audio
ENV STATIC_DIR=/app/web_dist

EXPOSE 8080
VOLUME ["/app/data"]

CMD ["/app/kotobalog-server"]
