package api

import (
	"context"
	"crypto/subtle"
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

const (
	userIDKey         contextKey = "user_id"
	sessionCookieName string     = "kotobalog_session"
)

type Allowlist map[string]bool

func ParseAllowlist(s string) Allowlist {
	a := Allowlist{}
	for _, entry := range strings.Split(s, ",") {
		if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
			a[entry] = true
		}
	}
	return a
}

func (a Allowlist) Allows(identity string) bool {
	return a[strings.ToLower(strings.TrimSpace(identity))]
}

type Config struct {
	JWTSecret          string
	Allowlist          string
	DevToken           string
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
	allow  Allowlist
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
		allow:  ParseAllowlist(cfg.Allowlist),
		router: http.NewServeMux(),
	}
	s.registerRoutes()
	return s, nil
}

func (s *Server) isAllowedOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	base := strings.TrimRight(strings.TrimSpace(s.cfg.BaseURL), "/")
	if base != "" && strings.EqualFold(origin, base) {
		return true
	}
	if base == "" || strings.HasPrefix(strings.ToLower(base), "http://localhost") || strings.HasPrefix(strings.ToLower(base), "http://127.0.0.1") {
		lower := strings.ToLower(origin)
		if strings.HasPrefix(lower, "http://localhost:") || lower == "http://localhost" ||
			strings.HasPrefix(lower, "http://127.0.0.1:") || lower == "http://127.0.0.1" {
			return true
		}
	}
	return false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")

	if origin := r.Header.Get("Origin"); s.isAllowedOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	}

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

	// Auth routes (OAuth + Allowlist only)
	s.router.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	s.router.HandleFunc("PUT /api/auth/me", s.requireAuth(s.handleUpdateMe))
	s.router.HandleFunc("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	s.router.HandleFunc("GET /api/auth/oauth/{provider}", s.handleOAuthRedirect)
	s.router.HandleFunc("GET /api/auth/oauth/{provider}/callback", s.handleOAuthCallback)

	// Notebook routes ("ことば帳" - shared across all allowlisted family members)
	s.router.HandleFunc("GET /api/notebooks", s.requireAuth(s.handleListNotebooks))
	s.router.HandleFunc("POST /api/notebooks", s.requireAuth(s.handleCreateNotebook))
	s.router.HandleFunc("PUT /api/notebooks/{id}", s.requireAuth(s.handleUpdateNotebook))

	// Word Entry routes ("ことば記録")
	s.router.HandleFunc("GET /api/notebooks/{id}/entries", s.requireAuth(s.handleListEntries))
	s.router.HandleFunc("POST /api/notebooks/{id}/entries", s.requireAuth(s.handleCreateEntry))
	s.router.HandleFunc("PUT /api/notebooks/{id}/entries/{entryId}", s.requireAuth(s.handleUpdateEntry))
	s.router.HandleFunc("DELETE /api/notebooks/{id}/entries/{entryId}", s.requireAuth(s.handleDeleteEntry))
	s.router.HandleFunc("POST /api/notebooks/{id}/entries/{entryId}/restore", s.requireAuth(s.handleRestoreEntry))
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
			indexFile := filepath.Join(s.cfg.StaticDir, "index.html")
			if _, err := os.Stat(indexFile); err == nil {
				http.ServeFile(w, r, indexFile)
				return
			}
			http.NotFound(w, r)
		})
	}
}

func (s *Server) generateToken(userID, identity string) (string, error) {
	var tokenVersion int
	_ = s.db.QueryRow(`SELECT token_version FROM users WHERE id = ?`, userID).Scan(&tokenVersion)
	claims := jwt.MapClaims{
		"sub":      userID,
		"identity": identity,
		"ver":      tokenVersion,
		"exp":      time.Now().Add(30 * 24 * time.Hour).Unix(),
		"iat":      time.Now().Unix(),
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
		} else if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
			tokenStr = c.Value
		} else if qToken := r.URL.Query().Get("token"); qToken != "" {
			tokenStr = qToken
		}

		if tokenStr == "" {
			writeError(w, http.StatusUnauthorized, "認証が必要です")
			return
		}

		// DevToken support for local/automated testing
		if s.cfg.DevToken != "" && subtle.ConstantTimeCompare([]byte(tokenStr), []byte(s.cfg.DevToken)) == 1 {
			uid, err := s.upsertOAuthUser("dev", "local", "dev:local", "dev@local", "開発ユーザー", "")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "開発ユーザー作成エラー")
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, uid)
			next(w, r.WithContext(ctx))
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

		// Check user still exists, token_version matches, and is still in ALLOWLIST
		var identity string
		var dbTokenVer int
		err = s.db.QueryRow(`SELECT identity, token_version FROM users WHERE id = ?`, sub).Scan(&identity, &dbTokenVer)
		if err != nil || !s.allow.Allows(identity) {
			writeError(w, http.StatusForbidden, "ALLOWLISTで許可されていないアカウントです")
			return
		}
		claimVer := 0
		if v, ok := claims["ver"].(float64); ok {
			claimVer = int(v)
		}
		if claimVer != dbTokenVer {
			writeError(w, http.StatusUnauthorized, "無効化されたトークンです")
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
