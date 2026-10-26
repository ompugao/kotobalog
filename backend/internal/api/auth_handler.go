package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string      `json:"token"`
	User  models.User `json:"user"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Email == "" || len(req.Password) < 6 || req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "メールアドレス、表示名、6文字以上のパスワードを入力してください")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "パスワード処理に失敗しました")
		return
	}

	user := models.User{
		ID:          uuid.NewString(),
		Email:       req.Email,
		DisplayName: req.DisplayName,
		CreatedAt:   time.Now().UTC(),
	}

	_, err = s.db.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, created_at) VALUES (?, ?, ?, ?, ?)`,
		user.ID, user.Email, string(hash), user.DisplayName, user.CreatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "このメールアドレスは既に登録されています")
			return
		}
		writeError(w, http.StatusInternalServerError, "ユーザー登録に失敗しました")
		return
	}

	token, err := s.generateToken(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "トークン生成に失敗しました")
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{
		Token: token,
		User:  user,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	var user models.User
	var passwordHash string

	err := s.db.QueryRow(
		`SELECT id, email, password_hash, display_name, avatar_url, created_at FROM users WHERE email = ?`,
		req.Email,
	).Scan(&user.ID, &user.Email, &passwordHash, &user.DisplayName, &user.AvatarURL, &user.CreatedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusUnauthorized, "メールアドレスまたはパスワードが正しくありません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "ログイン処理に失敗しました")
		return
	}

	if passwordHash == "" || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "メールアドレスまたはパスワードが正しくありません")
		return
	}

	token, err := s.generateToken(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "トークン生成に失敗しました")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		User:  user,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var user models.User
	err := s.db.QueryRow(
		`SELECT id, email, display_name, avatar_url, created_at FROM users WHERE id = ?`,
		uid,
	).Scan(&user.ID, &user.Email, &user.DisplayName, &user.AvatarURL, &user.CreatedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "ユーザーが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) oauthConfig(provider string) (*oauth2.Config, error) {
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		base = "http://localhost:8080"
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

func (s *Server) handleOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	conf, err := s.oauthConfig(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		redirectURI = "/"
	}
	url := conf.AuthCodeURL(redirectURI)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	conf, err := s.oauthConfig(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	code := r.URL.Query().Get("code")
	stateRedirect := r.URL.Query().Get("state")
	if code == "" {
		writeError(w, http.StatusBadRequest, "認可コードがありません")
		return
	}

	tok, err := conf.Exchange(context.Background(), code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "OAuthトークン交換に失敗しました")
		return
	}

	client := conf.Client(context.Background(), tok)
	var extID, email, name, avatar string

	if provider == "google" {
		resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
		if err != nil || resp.StatusCode != http.StatusOK {
			writeError(w, http.StatusUnauthorized, "Googleユーザー情報の取得に失敗しました")
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
		extID, email, name, avatar = info.ID, info.Email, info.Name, info.Picture
	} else if provider == "github" {
		resp, err := client.Get("https://api.github.com/user")
		if err != nil || resp.StatusCode != http.StatusOK {
			writeError(w, http.StatusUnauthorized, "GitHubユーザー情報の取得に失敗しました")
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
		name = info.Name
		if name == "" {
			name = info.Login
		}
		email = info.Email
		if email == "" {
			email = fmt.Sprintf("%s@users.noreply.github.com", info.Login)
		}
		avatar = info.AvatarURL
	}

	userID, err := s.upsertOAuthUser(provider, extID, email, name, avatar)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "OAuthユーザー保存に失敗しました")
		return
	}

	jwtToken, err := s.generateToken(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JWT生成に失敗しました")
		return
	}

	if stateRedirect == "" {
		stateRedirect = "/"
	}
	u, err := url.Parse(stateRedirect)
	if err != nil {
		stateRedirect = "/?token=" + url.QueryEscape(jwtToken)
	} else {
		q := u.Query()
		q.Set("token", jwtToken)
		u.RawQuery = q.Encode()
		stateRedirect = u.String()
	}
	http.Redirect(w, r, stateRedirect, http.StatusTemporaryRedirect)
}

func (s *Server) upsertOAuthUser(provider, extID, email, name, avatar string) (string, error) {
	col := "google_id"
	if provider == "github" {
		col = "github_id"
	}

	var id string
	err := s.db.QueryRow(fmt.Sprintf(`SELECT id FROM users WHERE %s = ? OR email = ?`, col), extID, email).Scan(&id)
	if err == sql.ErrNoRows {
		id = uuid.NewString()
		_, err = s.db.Exec(
			fmt.Sprintf(`INSERT INTO users (id, email, display_name, avatar_url, %s, created_at) VALUES (?, ?, ?, ?, ?, ?)`, col),
			id, email, name, avatar, extID, time.Now().UTC(),
		)
		return id, err
	} else if err != nil {
		return "", err
	}

	_, _ = s.db.Exec(fmt.Sprintf(`UPDATE users SET %s = ?, avatar_url = ? WHERE id = ?`, col), extID, avatar, id)
	return id, nil
}
