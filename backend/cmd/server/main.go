package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ompugao/kotobalog/backend/internal/api"
	"github.com/ompugao/kotobalog/backend/internal/db"
)

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	port := getenv("PORT", "8080")
	dbPath := getenv("DB_PATH", "./data/kotobalog.db")
	audioDir := getenv("AUDIO_DIR", "./data/audio")
	staticDir := getenv("STATIC_DIR", "./web_dist")

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer database.Close()

	cfg := api.Config{
		JWTSecret:          getenv("JWT_SECRET", "kotobalog-dev-secret-key"),
		AudioDir:           audioDir,
		StaticDir:          staticDir,
		BaseURL:            getenv("BASE_URL", "http://localhost:"+port),
		GoogleClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		GitHubClientID:     os.Getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
	}

	srv, err := api.NewServer(database, cfg)
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}

	log.Printf("KotobaLog server listening on http://0.0.0.0:%s", port)
	if err := http.ListenAndServe(":"+port, srv); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
