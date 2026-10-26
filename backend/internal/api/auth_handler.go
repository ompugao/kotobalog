package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

const oauthStateCookieName = "kotobalog_oauth_state"

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var user models.User
	err := s.db.QueryRow(
		`SELECT id, identity, email, display_name, avatar_url, created_at FROM users WHERE id = ?`,
		uid,
	).Scan(&user.ID, &user.Identity, &user.Email, &user.DisplayName, &user.AvatarURL, &user.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "ユーザーが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "表示名を入力してください")
		return
	}
	if _, err := s.db.Exec(`UPDATE users SET display_name = ? WHERE id = ?`, name, uid); err != nil {
		writeError(w, http.StatusInternalServerError, "表示名の更新に失敗しました")
		return
	}
	s.handleMe(w, r)
}

func (s *Server) oauthConfig(provider string) (*oauth2.Config, error) {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		base = "http://localhost:18081"
	}
	switch provider {
	case "google":
		if s.cfg.GoogleClientID == "" || s.cfg.GoogleClientSecret == "" {
			return nil, fmt.Errorf("Google OAuthが設定されていません")
		}
		return &oauth2.Config{
			ClientID:     s.cfg.GoogleClientID,
			ClientSecret: s.cfg.GoogleClientSecret,
			RedirectURL:  base + "/api/auth/oauth/google/callback",
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     google.Endpoint,
		}, nil
	case "github":
		if s.cfg.GitHubClientID == "" || s.cfg.GitHubClientSecret == "" {
			return nil, fmt.Errorf("GitHub OAuthが設定されていません")
		}
		return &oauth2.Config{
			ClientID:     s.cfg.GitHubClientID,
			ClientSecret: s.cfg.GitHubClientSecret,
			RedirectURL:  base + "/api/auth/oauth/github/callback",
			Scopes:       []string{"read:user", "user:email"},
			Endpoint:     github.Endpoint,
		}, nil
	default:
		return nil, fmt.Errorf("未対応のOAuthプロバイダです: %s", provider)
	}
}

func (s *Server) sanitizeRedirectURI(rawURI string) string {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return "/"
	}
	if rawURI == "kotobalog://auth" {
		return rawURI
	}
	if strings.HasPrefix(rawURI, "/") && !strings.HasPrefix(rawURI, "//") && !strings.HasPrefix(rawURI, "/\\") {
		return rawURI
	}
	base := strings.TrimSpace(s.cfg.BaseURL)
	if base != "" {
		baseU, err1 := url.Parse(base)
		targetU, err2 := url.Parse(rawURI)
		if err1 == nil && err2 == nil && baseU.Scheme != "" && baseU.Host != "" &&
			strings.EqualFold(baseU.Scheme, targetU.Scheme) &&
			strings.EqualFold(baseU.Host, targetU.Host) {
			return rawURI
		}
	}
	return "/"
}

func (s *Server) generateOAuthState(redirectURI string) (stateToken string, nonce string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	nonce = hex.EncodeToString(b)
	claims := jwt.MapClaims{
		"nonce":        nonce,
		"redirect_uri": redirectURI,
		"exp":          time.Now().Add(10 * time.Minute).Unix(),
		"iat":          time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.cfg.JWTSecret))
	return signed, nonce, err
}

func (s *Server) verifyOAuthState(r *http.Request) (string, error) {
	stateToken := r.URL.Query().Get("state")
	if stateToken == "" {
		return "/", fmt.Errorf("missing state")
	}
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || cookie.Value == "" {
		return "/", fmt.Errorf("missing state cookie")
	}

	tok, err := jwt.Parse(stateToken, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !tok.Valid {
		return "/", fmt.Errorf("invalid state token")
	}

	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "/", fmt.Errorf("invalid state claims")
	}

	nonce, _ := claims["nonce"].(string)
	if nonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(cookie.Value)) != 1 {
		return "/", fmt.Errorf("state nonce mismatch")
	}

	rawRedirect, _ := claims["redirect_uri"].(string)
	return s.sanitizeRedirectURI(rawRedirect), nil
}

func (s *Server) handleOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	conf, err := s.oauthConfig(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	redirectURI := s.sanitizeRedirectURI(r.URL.Query().Get("redirect_uri"))
	stateToken, nonce, err := s.generateOAuthState(redirectURI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "OAuth stateの生成に失敗しました")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    nonce,
		Path:     "/api/auth/oauth",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   strings.HasPrefix(strings.ToLower(s.cfg.BaseURL), "https://"),
		SameSite: http.SameSiteLaxMode,
	})

	authURL := conf.AuthCodeURL(stateToken)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

func redirectWithQuery(w http.ResponseWriter, r *http.Request, baseRedirect string, params map[string]string) {
	if baseRedirect == "" {
		baseRedirect = "/"
	}
	u, err := url.Parse(baseRedirect)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	conf, err := s.oauthConfig(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	stateRedirect, err := s.verifyOAuthState(r)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/api/auth/oauth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   strings.HasPrefix(strings.ToLower(s.cfg.BaseURL), "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	if err != nil {
		redirectWithQuery(w, r, "/", map[string]string{"error": "invalid_state"})
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "missing_code"})
		return
	}

	tok, err := conf.Exchange(context.Background(), code)
	if err != nil {
		redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "exchange_failed"})
		return
	}

	client := conf.Client(context.Background(), tok)
	var extID, identity, email, name, avatar string

	if provider == "google" {
		resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
		if err != nil || resp.StatusCode != http.StatusOK {
			redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "userinfo_failed"})
			return
		}
		defer resp.Body.Close()
		var info struct {
			ID      string `json:"id"`
			Email   string `json:"email"`
			Name    string `json:"name"`
			Picture string `json:"picture"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&info)
		extID = info.ID
		email = strings.ToLower(strings.TrimSpace(info.Email))
		identity = "google:" + email
		name = info.Name
		if name == "" {
			name = email
		}
		avatar = info.Picture
	} else if provider == "github" {
		resp, err := client.Get("https://api.github.com/user")
		if err != nil || resp.StatusCode != http.StatusOK {
			redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "userinfo_failed"})
			return
		}
		defer resp.Body.Close()
		var info struct {
			ID        int64  `json:"id"`
			Login     string `json:"login"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			AvatarURL string `json:"avatar_url"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&info)
		extID = fmt.Sprintf("%d", info.ID)
		login := strings.ToLower(strings.TrimSpace(info.Login))
		identity = "github:" + login
		name = info.Name
		if name == "" {
			name = info.Login
		}
		email = info.Email
		if email == "" {
			email = fmt.Sprintf("%s@users.noreply.github.com", login)
		}
		avatar = info.AvatarURL
	}

	if !s.allow.Allows(identity) {
		redirectWithQuery(w, r, stateRedirect, map[string]string{
			"error":    "not_allowed",
			"identity": identity,
		})
		return
	}

	userID, err := s.upsertOAuthUser(provider, extID, identity, email, name, avatar)
	if err != nil {
		redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "db_error"})
		return
	}

	jwtToken, err := s.generateToken(userID, identity)
	if err != nil {
		redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "token_error"})
		return
	}

	redirectWithQuery(w, r, stateRedirect, map[string]string{"token": jwtToken})
}

func (s *Server) upsertOAuthUser(provider, extID, identity, email, name, avatar string) (string, error) {
	col := "google_id"
	if provider == "github" {
		col = "github_id"
	} else if provider == "dev" {
		var id string
		err := s.db.QueryRow(`SELECT id FROM users WHERE identity = ?`, identity).Scan(&id)
		if err == sql.ErrNoRows {
			id = uuid.NewString()
			_, err = s.db.Exec(
				`INSERT INTO users (id, identity, email, display_name, avatar_url, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				id, identity, email, name, avatar, time.Now().UTC(),
			)
		}
		return id, err
	}

	var id string
	err := s.db.QueryRow(fmt.Sprintf(`SELECT id FROM users WHERE %s = ? OR identity = ?`, col), extID, identity).Scan(&id)
	if err == sql.ErrNoRows {
		id = uuid.NewString()
		_, err = s.db.Exec(
			fmt.Sprintf(`INSERT INTO users (id, identity, email, display_name, avatar_url, %s, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, col),
			id, identity, email, name, avatar, extID, time.Now().UTC(),
		)
		return id, err
	} else if err != nil {
		return "", err
	}

	_, _ = s.db.Exec(fmt.Sprintf(`UPDATE users SET identity = ?, email = ?, avatar_url = ?, %s = ? WHERE id = ?`, col), identity, email, avatar, extID, id)
	return id, nil
}
