package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"gorm.io/gorm"
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

type authExchangeRecord struct {
	userID              string
	identity            string
	codeChallenge       string
	codeChallengeMethod string
	redirectURI         string
	expiresAt           time.Time
}

type Server struct {
	db            *gorm.DB
	cfg           Config
	allow         Allowlist
	router        *http.ServeMux
	exchangeMu    sync.Mutex
	exchangeCodes map[string]authExchangeRecord
}

func (s *Server) safeAudioPath(relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", errors.New("empty path")
	}
	cleanBase, err := filepath.Abs(s.cfg.AudioDir)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(filepath.Join(cleanBase, relPath))
	rel, err := filepath.Rel(cleanBase, target)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return "", errors.New("path traversal detected")
	}
	return target, nil
}

func NewServer(database *gorm.DB, cfg Config) (*Server, error) {
	if cfg.JWTSecret == "" || len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWTSecret must be provided and be at least 32 characters")
	}
	if cfg.AudioDir == "" {
		cfg.AudioDir = "./data/audio"
	}
	if err := os.MkdirAll(cfg.AudioDir, 0755); err != nil {
		return nil, fmt.Errorf("create audio dir: %w", err)
	}

	s := &Server{
		db:            database,
		cfg:           cfg,
		allow:         ParseAllowlist(cfg.Allowlist),
		router:        http.NewServeMux(),
		exchangeCodes: make(map[string]authExchangeRecord),
	}
	s.migrateLegacyRawAudio()
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

	// For local development, allow specific local dev ports only instead of arbitrary wildcard ports
	baseU, err := url.Parse(base)
	isLocalBase := base == "" || (err == nil && (baseU.Hostname() == "localhost" || baseU.Hostname() == "127.0.0.1"))
	if isLocalBase {
		lower := strings.ToLower(origin)
		u, err := url.Parse(lower)
		if err != nil {
			return false
		}
		host := u.Hostname()
		if host != "localhost" && host != "127.0.0.1" {
			return false
		}
		port := u.Port()
		if baseU != nil && baseU.Port() != "" && port == baseU.Port() {
			return true
		}
		// Allow Flutter web dev server (8081) and standard KotobaLog ports (18081, 8080)
		if port == "8081" || port == "18081" || port == "8080" {
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
	s.router.HandleFunc("POST /api/auth/exchange", s.handleAuthExchange)

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
	s.router.HandleFunc("POST /api/notebooks/{id}/entries/{entryId}/media-ticket", s.requireAuth(s.handleCreateMediaTicket))
	s.router.HandleFunc("GET /api/notebooks/{id}/entries/{entryId}/audio", s.requireMediaAuth(s.handleGetEntryAudio))
	s.router.HandleFunc("GET /api/notebooks/{id}/entries/{entryId}/raw-audio", s.requireMediaAuth(s.handleGetEntryRawAudio))

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
	var user models.User
	_ = s.db.Select("token_version").Where("id = ?", userID).First(&user).Error
	claims := jwt.MapClaims{
		"sub":      userID,
		"identity": identity,
		"ver":      user.TokenVersion,
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
		var user models.User
		err = s.db.Select("identity", "token_version").Where("id = ?", sub).First(&user).Error
		if err != nil || !s.allow.Allows(user.Identity) {
			writeError(w, http.StatusForbidden, "ALLOWLISTで許可されていないアカウントです")
			return
		}
		claimVer := 0
		if v, ok := claims["ver"].(float64); ok {
			claimVer = int(v)
		}
		if claimVer != user.TokenVersion {
			writeError(w, http.StatusUnauthorized, "無効化されたトークンです")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, sub)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) requireMediaAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr := ""
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		} else if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
			tokenStr = c.Value
		}

		if tokenStr != "" {
			s.requireAuth(next)(w, r)
			return
		}

		// Check short-lived scoped media ticket
		ticketStr := r.URL.Query().Get("ticket")
		if ticketStr == "" {
			writeError(w, http.StatusUnauthorized, "認証が必要です")
			return
		}

		token, err := jwt.Parse(ticketStr, func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(s.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "無効なメディアチケットです")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, http.StatusUnauthorized, "無効なクレームです")
			return
		}

		if scope, _ := claims["scope"].(string); scope != "media" {
			writeError(w, http.StatusForbidden, "チケットのスコープが無効です")
			return
		}

		notebookID := r.PathValue("id")
		entryID := r.PathValue("entryId")
		if claims["notebook_id"] != notebookID || claims["entry_id"] != entryID {
			writeError(w, http.StatusForbidden, "チケットの対象エントリが一致しません")
			return
		}

		sub, _ := claims["sub"].(string)
		if sub == "" {
			writeError(w, http.StatusUnauthorized, "ユーザーIDが見つかりません")
			return
		}

		var user models.User
		if err := s.db.Select("identity").Where("id = ?", sub).First(&user).Error; err != nil || !s.allow.Allows(user.Identity) {
			writeError(w, http.StatusForbidden, "許可されていないユーザーです")
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, sub)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleCreateMediaTicket(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	claims := jwt.MapClaims{
		"sub":         uid,
		"scope":       "media",
		"notebook_id": notebookID,
		"entry_id":    entryID,
		"exp":         time.Now().Add(2 * time.Minute).Unix(),
		"iat":         time.Now().Unix(),
	}
	ticketToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := ticketToken.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "チケット生成エラー")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     signed,
		"expires_in": 120,
	})
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
