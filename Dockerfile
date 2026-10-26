# Stage 1: Build Go Backend
FROM golang:1.27 AS go-builder
WORKDIR /workspace/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kotobalog-server ./cmd/server \
    && mkdir -p /data

# Stage 2: Final Lightweight Runtime Image for NAS
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=go-builder /kotobalog-server /app/kotobalog-server
COPY --from=go-builder --chown=nonroot:nonroot /data /data
COPY backend/web_dist /app/web_dist

ENV PORT=8080
ENV DB_PATH=/data/kotobalog.db
ENV AUDIO_DIR=/data/audio
ENV STATIC_DIR=/app/web_dist

EXPOSE 8080
VOLUME ["/data"]
USER nonroot
ENTRYPOINT ["/app/kotobalog-server"]
