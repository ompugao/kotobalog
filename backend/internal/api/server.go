package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const userIDKey contextKey = "user_id"

type Config struct {
	JWTSecret          string
	AudioDir           string
	StaticDir          string
	BaseURL            string
	GoogleClientID     string
	GoogleClientSecret string
	GitHubClientID     string
	GitHubClientSecret string
}

type Server struct {
	db     *sql.DB
	cfg    Config
	router *http.ServeMux
}

func NewServer(database *sql.DB, cfg Config) (*Server, error) {
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "kotobalog-default-dev-secret-change-me"
	}
	if cfg.AudioDir == "" {
		cfg.AudioDir = "./data/audio"
	}
	if err := os.MkdirAll(cfg.AudioDir, 0755); err != nil {
		return nil, fmt.Errorf("create audio dir: %w", err)
	}

	s := &Server{
		db:     database,
		cfg:    cfg,
		router: http.NewServeMux(),
	}
	s.registerRoutes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS headers for Flutter Web & Android clients
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.router.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Health & Providers
	s.router.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"oauth": map[string]bool{
				"google": s.cfg.GoogleClientID != "" && s.cfg.GoogleClientSecret != "",
				"github": s.cfg.GitHubClientID != "" && s.cfg.GitHubClientSecret != "",
			},
		})
	})

	// Auth routes
	s.router.HandleFunc("POST /api/auth/register", s.handleRegister)
	s.router.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.router.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	s.router.HandleFunc("GET /api/auth/oauth/{provider}", s.handleOAuthRedirect)
	s.router.HandleFunc("GET /api/auth/oauth/{provider}/callback", s.handleOAuthCallback)

	// Notebook routes ("ことば帳")
	s.router.HandleFunc("GET /api/notebooks", s.requireAuth(s.handleListNotebooks))
	s.router.HandleFunc("POST /api/notebooks", s.requireAuth(s.handleCreateNotebook))
	s.router.HandleFunc("PUT /api/notebooks/{id}", s.requireAuth(s.handleUpdateNotebook))
	s.router.HandleFunc("POST /api/notebooks/{id}/invites", s.requireAuth(s.handleCreateInvite))
	s.router.HandleFunc("POST /api/notebooks/join", s.requireAuth(s.handleJoinNotebook))

	// Word Entry routes ("ことば記録")
	s.router.HandleFunc("GET /api/notebooks/{id}/entries", s.requireAuth(s.handleListEntries))
	s.router.HandleFunc("POST /api/notebooks/{id}/entries", s.requireAuth(s.handleCreateEntry))
	s.router.HandleFunc("PUT /api/notebooks/{id}/entries/{entryId}", s.requireAuth(s.handleUpdateEntry))
	s.router.HandleFunc("DELETE /api/notebooks/{id}/entries/{entryId}", s.requireAuth(s.handleDeleteEntry))
	s.router.HandleFunc("GET /api/notebooks/{id}/entries/{entryId}/audio", s.requireAuth(s.handleGetEntryAudio))

	// Static files for Flutter Web if StaticDir exists
	if s.cfg.StaticDir != "" {
		fs := http.FileServer(http.Dir(s.cfg.StaticDir))
		s.router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusNotFound, "API endpoint not found")
				return
			}
			fullPath := filepath.Join(s.cfg.StaticDir, filepath.Clean(r.URL.Path))
			if info, err := os.Stat(fullPath); err == nil && !info.IsDir() {
				fs.ServeHTTP(w, r)
				return
			}
			// SPA fallback to index.html
			indexFile := filepath.Join(s.cfg.StaticDir, "index.html")
			if _, err := os.Stat(indexFile); err == nil {
				http.ServeFile(w, r, indexFile)
				return
			}
			http.NotFound(w, r)
		})
	}
}

func (s *Server) generateToken(userID string) (string, error) {
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		} else if qToken := r.URL.Query().Get("token"); qToken != "" {
			// Support ?token=... for audio player URLs on Web/Android
			tokenStr = qToken
		}

		if tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "認証が必要です")
			return
		}

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(s.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "無効なトークンです")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, http.StatusUnauthorized, "無効なクレームです")
			return
		}

		sub, _ := claims["sub"].(string)
		if sub == "" {
			writeError(w, http.StatusUnauthorized, "ユーザーIDが見つかりません")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, sub)
		next(w, r.WithContext(ctx))
	}
}

func currentUserID(r *http.Request) string {
	val, _ := r.Context().Value(userIDKey).(string)
	return val
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
