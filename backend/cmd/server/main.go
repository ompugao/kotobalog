package main

import (
	"crypto/rand"
	"encoding/hex"
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
	if sqlDB, err := database.DB(); err == nil {
		defer sqlDB.Close()
	}

	allowlist := os.Getenv("ALLOWLIST")
	if allowlist == "" {
		log.Printf("WARNING: ALLOWLIST is empty; nobody can sign in via OAuth")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	devToken := os.Getenv("DEV_TOKEN")
	isWeakSecret := jwtSecret == "" ||
		jwtSecret == "please-change-this-secret-in-production" ||
		jwtSecret == "kotobalog-dev-secret-key" ||
		jwtSecret == "kotobalog-default-dev-secret-change-me" ||
		len(jwtSecret) < 32

	if isWeakSecret {
		if devToken == "" {
			log.Fatalf("FATAL: JWT_SECRET must be set to a strong secret of at least 32 characters")
		}
		// In DEV_TOKEN mode, generate an unguessable ephemeral 32-byte secret instead of using a known hardcoded string
		randomBytes := make([]byte, 32)
		if _, err := rand.Read(randomBytes); err != nil {
			log.Fatalf("FATAL: failed to generate ephemeral jwt secret: %v", err)
		}
		jwtSecret = hex.EncodeToString(randomBytes)
		log.Printf("WARNING: generated unguessable ephemeral JWT_SECRET for DEV_TOKEN session")
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
