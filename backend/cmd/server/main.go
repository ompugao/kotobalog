package main

import (
	"log"
	"net/http"
	"os"
	"time"

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
	port := getenv("PORT", "18081")
	dbPath := getenv("DB_PATH", "./data/kotobalog.db")
	audioDir := getenv("AUDIO_DIR", "./data/audio")
	staticDir := getenv("STATIC_DIR", "./web_dist")

	database, err := db.Open(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer database.Close()

	allowlist := os.Getenv("ALLOWLIST")
	if allowlist == "" {
		log.Printf("WARNING: ALLOWLIST is empty; nobody can sign in via OAuth")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	devToken := os.Getenv("DEV_TOKEN")
	if jwtSecret == "" || jwtSecret == "please-change-this-secret-in-production" ||
		jwtSecret == "kotobalog-dev-secret-key" || jwtSecret == "kotobalog-default-dev-secret-change-me" ||
		len(jwtSecret) < 32 {
		if devToken == "" {
			log.Fatalf("FATAL: JWT_SECRET must be set to a strong secret of at least 32 characters")
		}
		log.Printf("WARNING: using insecure JWT_SECRET in DEV_TOKEN mode")
		if jwtSecret == "" {
			jwtSecret = "kotobalog-dev-secret-key"
		}
	}

	cfg := api.Config{
		JWTSecret:          jwtSecret,
		Allowlist:          allowlist,
		DevToken:           devToken,
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

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("KotobaLog server listening on http://0.0.0.0:%s", port)
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
