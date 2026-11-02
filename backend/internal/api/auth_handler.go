package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"gorm.io/gorm"
)

const oauthStateCookieName = "kotobalog_oauth_state"

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   strings.HasPrefix(strings.ToLower(s.cfg.BaseURL), "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var user models.User
	if err := s.db.Where("id = ?", uid).First(&user).Error; err != nil {
		writeError(w, http.StatusNotFound, "ユーザーが見つかりません")
		return
	}
	if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		s.setSessionCookie(w, strings.TrimPrefix(authHeader, "Bearer "), 30*24*3600)
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
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
	if err := s.db.Model(&models.User{}).Where("id = ?", uid).Update("display_name", name).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "表示名の更新に失敗しました")
		return
	}
	s.handleMe(w, r)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	_ = s.db.Model(&models.User{}).Where("id = ?", uid).UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
	s.setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
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

func (s *Server) generateOAuthState(redirectURI, codeChallenge, codeChallengeMethod string) (stateToken string, nonce string, err error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	nonce = hex.EncodeToString(b)
	claims := jwt.MapClaims{
		"nonce":                 nonce,
		"redirect_uri":          redirectURI,
		"code_challenge":        codeChallenge,
		"code_challenge_method": codeChallengeMethod,
		"exp":                   time.Now().Add(10 * time.Minute).Unix(),
		"iat":                   time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.cfg.JWTSecret))
	return signed, nonce, err
}

func (s *Server) verifyOAuthState(r *http.Request) (string, string, string, error) {
	stateToken := r.URL.Query().Get("state")
	if stateToken == "" {
		return "/", "", "", fmt.Errorf("missing state")
	}
	cookie, err := r.Cookie(oauthStateCookieName)
	if err != nil || cookie.Value == "" {
		return "/", "", "", fmt.Errorf("missing state cookie")
	}

	tok, err := jwt.Parse(stateToken, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !tok.Valid {
		return "/", "", "", fmt.Errorf("invalid state token")
	}

	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return "/", "", "", fmt.Errorf("invalid state claims")
	}

	nonce, _ := claims["nonce"].(string)
	if nonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(cookie.Value)) != 1 {
		return "/", "", "", fmt.Errorf("state nonce mismatch")
	}

	rawRedirect, _ := claims["redirect_uri"].(string)
	codeChallenge, _ := claims["code_challenge"].(string)
	codeChallengeMethod, _ := claims["code_challenge_method"].(string)
	return s.sanitizeRedirectURI(rawRedirect), codeChallenge, codeChallengeMethod, nil
}

func (s *Server) handleOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	conf, err := s.oauthConfig(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	redirectURI := s.sanitizeRedirectURI(r.URL.Query().Get("redirect_uri"))
	codeChallenge := strings.TrimSpace(r.URL.Query().Get("code_challenge"))
	codeChallengeMethod := strings.TrimSpace(r.URL.Query().Get("code_challenge_method"))
	if codeChallenge != "" && codeChallengeMethod == "" {
		codeChallengeMethod = "S256"
	}
	stateToken, nonce, err := s.generateOAuthState(redirectURI, codeChallenge, codeChallengeMethod)
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

	stateRedirect, codeChallenge, codeChallengeMethod, err := s.verifyOAuthState(r)
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
			ID            string `json:"id"`
			Email         string `json:"email"`
			VerifiedEmail bool   `json:"verified_email"`
			Name          string `json:"name"`
			Picture       string `json:"picture"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.ID == "" || info.Email == "" || !info.VerifiedEmail {
			redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "email_not_verified"})
			return
		}
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
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.ID == 0 || info.Login == "" {
			redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "userinfo_failed"})
			return
		}
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

	// Generate single-use authorization exchange code (32 random bytes)
	codeBytes := make([]byte, 32)
	if _, err := rand.Read(codeBytes); err != nil {
		redirectWithQuery(w, r, stateRedirect, map[string]string{"error": "server_error"})
		return
	}
	authCode := hex.EncodeToString(codeBytes)

	s.exchangeMu.Lock()
	now := time.Now()
	for k, v := range s.exchangeCodes {
		if now.After(v.expiresAt) {
			delete(s.exchangeCodes, k)
		}
	}
	s.exchangeCodes[authCode] = authExchangeRecord{
		userID:              userID,
		identity:            identity,
		codeChallenge:       codeChallenge,
		codeChallengeMethod: codeChallengeMethod,
		redirectURI:         stateRedirect,
		expiresAt:           now.Add(2 * time.Minute),
	}
	s.exchangeMu.Unlock()

	// Set session cookie for same-origin web
	if jwtToken, err := s.generateToken(userID, identity); err == nil {
		s.setSessionCookie(w, jwtToken, 30*24*3600)
	}

	// Redirect with ephemeral single-use code instead of long-lived token
	redirectWithQuery(w, r, stateRedirect, map[string]string{"code": authCode})
}

func (s *Server) handleAuthExchange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "codeが必要です")
		return
	}

	s.exchangeMu.Lock()
	rec, exists := s.exchangeCodes[req.Code]
	if exists {
		delete(s.exchangeCodes, req.Code) // Single use
	}
	s.exchangeMu.Unlock()

	if !exists || time.Now().After(rec.expiresAt) {
		writeError(w, http.StatusBadRequest, "無効または期限切れの認証コードです")
		return
	}

	// If PKCE was requested or if the redirect was a custom mobile scheme, verify code_verifier
	if rec.codeChallenge != "" || rec.redirectURI == "kotobalog://auth" {
		if rec.codeChallenge == "" {
			writeError(w, http.StatusBadRequest, "PKCE code_challengeが設定されていません")
			return
		}
		if req.CodeVerifier == "" {
			writeError(w, http.StatusBadRequest, "code_verifierが必要です")
			return
		}

		var verified bool
		switch rec.codeChallengeMethod {
		case "S256":
			hash := sha256.Sum256([]byte(req.CodeVerifier))
			computed := base64.RawURLEncoding.EncodeToString(hash[:])
			verified = subtle.ConstantTimeCompare([]byte(computed), []byte(rec.codeChallenge)) == 1
		case "plain":
			verified = subtle.ConstantTimeCompare([]byte(req.CodeVerifier), []byte(rec.codeChallenge)) == 1
		default:
			writeError(w, http.StatusBadRequest, "未対応のcode_challenge_methodです")
			return
		}

		if !verified {
			writeError(w, http.StatusBadRequest, "code_verifierが一致しません")
			return
		}
	}

	if !s.allow.Allows(rec.identity) {
		writeError(w, http.StatusForbidden, "ALLOWLISTで許可されていないアカウントです")
		return
	}

	var user models.User
	if err := s.db.Where("id = ?", rec.userID).First(&user).Error; err != nil {
		writeError(w, http.StatusNotFound, "ユーザーが見つかりません")
		return
	}

	token, err := s.generateToken(user.ID, user.Identity)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "トークン生成エラー")
		return
	}

	s.setSessionCookie(w, token, 30*24*3600)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  user,
	})
}

func (s *Server) upsertOAuthUser(provider, extID, identity, email, name, avatar string) (string, error) {
	if provider == "dev" {
		var user models.User
		err := s.db.Where("identity = ?", identity).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			user = models.User{
				ID:          uuid.NewString(),
				Identity:    identity,
				Email:       email,
				DisplayName: name,
				AvatarURL:   avatar,
				CreatedAt:   time.Now().UTC(),
			}
			err = s.db.Create(&user).Error
		}
		return user.ID, err
	}

	var user models.User
	var err error
	if provider == "github" {
		err = s.db.Where("github_id = ?", extID).First(&user).Error
	} else {
		err = s.db.Where("google_id = ?", extID).First(&user).Error
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = s.db.Where("identity = ?", identity).First(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			extCopy := extID
			user = models.User{
				ID:          uuid.NewString(),
				Identity:    identity,
				Email:       email,
				DisplayName: name,
				AvatarURL:   avatar,
				CreatedAt:   time.Now().UTC(),
			}
			if provider == "github" {
				user.GitHubID = &extCopy
			} else {
				user.GoogleID = &extCopy
			}
			if err := s.db.Create(&user).Error; err != nil {
				return "", err
			}
			return user.ID, nil
		} else if err != nil {
			return "", err
		}

		existingExtID := ""
		if provider == "github" && user.GitHubID != nil {
			existingExtID = *user.GitHubID
		} else if provider != "github" && user.GoogleID != nil {
			existingExtID = *user.GoogleID
		}
		if existingExtID != "" && existingExtID != extID {
			return "", fmt.Errorf("identity %s is already bound to a different %s account", identity, provider)
		}
	} else if err != nil {
		return "", err
	}

	updates := map[string]any{
		"identity":   identity,
		"email":      email,
		"avatar_url": avatar,
	}
	if provider == "github" {
		updates["github_id"] = extID
	} else {
		updates["google_id"] = extID
	}
	_ = s.db.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error
	return user.ID, nil
}
