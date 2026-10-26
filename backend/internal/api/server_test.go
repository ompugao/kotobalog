package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ompugao/kotobalog/backend/internal/db"
	"github.com/ompugao/kotobalog/backend/internal/models"
)

func setupTestServer(t *testing.T, allowlist string) *Server {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	srv, err := NewServer(database, Config{
		JWTSecret: "test-secret",
		Allowlist: allowlist,
		AudioDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	return srv
}

func doJSON(t *testing.T, srv http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestAllowlistAndSharedNotebooksFlow(t *testing.T) {
	srv := setupTestServer(t, "github:ompugao, google:mama@example.com")

	// 1. Upsert allowlisted Papa (github:ompugao) & Mama (google:mama@example.com), and unallowed Stranger
	papaID, err := srv.upsertOAuthUser("github", "1001", "github:ompugao", "papa@example.com", "パパ", "")
	if err != nil {
		t.Fatalf("upsert papa: %v", err)
	}
	papaToken, _ := srv.generateToken(papaID, "github:ompugao")

	mamaID, err := srv.upsertOAuthUser("google", "2002", "google:mama@example.com", "mama@example.com", "ママ", "")
	if err != nil {
		t.Fatalf("upsert mama: %v", err)
	}
	mamaToken, _ := srv.generateToken(mamaID, "google:mama@example.com")

	strangerID, err := srv.upsertOAuthUser("github", "9999", "github:stranger", "stranger@example.com", "他人", "")
	if err != nil {
		t.Fatalf("upsert stranger: %v", err)
	}
	strangerToken, _ := srv.generateToken(strangerID, "github:stranger")

	// 2. Stranger is rejected by requireAuth because not in ALLOWLIST
	rec := doJSON(t, srv, http.MethodGet, "/api/notebooks", strangerToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unallowed user, got %d", rec.Code)
	}

	// 3. Mama creates a notebook ("はるくんのことば帳")
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks", mamaToken, map[string]string{
		"child_name":  "はるくん",
		"birth_date":  "2025-05-10",
		"theme_color": "#4DB6AC",
		"icon_emoji":  "🦖",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create notebook, got %d: %s", rec.Code, rec.Body.String())
	}
	var nb models.Notebook
	_ = json.NewDecoder(rec.Body).Decode(&nb)
	if nb.ChildName != "はるくん" || len(nb.Members) != 2 {
		t.Fatalf("expected notebook with 2 allowlisted family members, got: %+v", nb)
	}

	// 4. Papa automatically sees the notebook without invite code and records a word with audio
	rec = doJSON(t, srv, http.MethodGet, "/api/notebooks", papaToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on papa list notebooks, got %d", rec.Code)
	}

	dummyWav := []byte("RIFFxxxxWAVEfmt ")
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaToken, map[string]any{
		"word":              "ぶーぶー",
		"meaning":           "パトカーや救急車を見つけたときに指差して言った",
		"spoken_at":         "2026-10-26T10:30:00Z",
		"audio_base64":      base64.StdEncoding.EncodeToString(dummyWav),
		"audio_mime":        "audio/wav",
		"audio_duration_ms": 2400,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create entry, got %d: %s", rec.Code, rec.Body.String())
	}
	var createdEntry models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&createdEntry)
	if !createdEntry.HasAudio || createdEntry.CreatorName != "パパ" {
		t.Fatalf("unexpected entry: %+v", createdEntry)
	}

	// 5. Mama searches entries and fetches audio
	rec = doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries?q=救急車", mamaToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list entries, got %d", rec.Code)
	}
	var entries []models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&entries)
	if len(entries) != 1 || entries[0].Word != "ぶーぶー" {
		t.Fatalf("unexpected search results: %+v", entries)
	}

	// 6. Fetch audio using ?token= query param
	audioReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/audio?token="+mamaToken, nil)
	audioRec := httptest.NewRecorder()
	srv.ServeHTTP(audioRec, audioReq)
	if audioRec.Code != http.StatusOK || !bytes.Equal(audioRec.Body.Bytes(), dummyWav) {
		t.Fatalf("expected 200 and audio bytes, got %d", audioRec.Code)
	}
	if gotMime := audioRec.Header().Get("Content-Type"); !strings.HasPrefix(gotMime, "audio/wav") {
		t.Fatalf("expected audio/wav Content-Type, got %q", gotMime)
	}
	if audioRec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options: nosniff")
	}

	// 6.5 Mama updates entry memo/word and trims audio
	trimmedWav := []byte("RIFFtrimWAVEfmt ")
	updateRec := doJSON(t, srv, http.MethodPut, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID, mamaToken, map[string]any{
		"word":              "ぶーぶー！",
		"meaning":           "救急車と消防車の両方を見て大喜びで言った（メモ追記）",
		"spoken_at":         "2026-10-26T11:00:00Z",
		"audio_base64":      base64.StdEncoding.EncodeToString(trimmedWav),
		"audio_mime":        "audio/wav",
		"audio_duration_ms": 1200,
	})
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on update entry, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updatedEntry models.WordEntry
	_ = json.NewDecoder(updateRec.Body).Decode(&updatedEntry)
	if updatedEntry.Word != "ぶーぶー！" || !strings.Contains(updatedEntry.Meaning, "メモ追記") || updatedEntry.AudioDurationMs != 1200 {
		t.Fatalf("unexpected updated entry: %+v", updatedEntry)
	}

	// 7. Uploading malicious audio_mime (e.g., text/html) must be sanitized to audio/wav
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaToken, map[string]any{
		"word":         "てすと",
		"audio_base64": base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>")),
		"audio_mime":   "text/html",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	var xssEntry models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&xssEntry)
	if xssEntry.AudioMime != "audio/wav" {
		t.Fatalf("expected audio_mime to be sanitized to audio/wav, got %q", xssEntry.AudioMime)
	}

	// 8. Logout revokes issued JWT on server
	logoutRec := doJSON(t, srv, http.MethodPost, "/api/auth/logout", papaToken, nil)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on logout, got %d", logoutRec.Code)
	}
	afterLogoutRec := doJSON(t, srv, http.MethodGet, "/api/notebooks", papaToken, nil)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after token revocation, got %d", afterLogoutRec.Code)
	}
}

func TestOAuthStateCSRFAndOpenRedirectProtection(t *testing.T) {
	srv := setupTestServer(t, "github:ompugao")
	srv.cfg.BaseURL = "https://kotobalog.example.com"
	srv.cfg.GitHubClientID = "dummy-id"
	srv.cfg.GitHubClientSecret = "dummy-secret"

	// 1. Open redirect attempt via redirect_uri should be sanitized to "/"
	req := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/github?redirect_uri=https://evil.example.com/steal", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("invalid redirect Location: %v", err)
	}
	stateParam := loc.Query().Get("state")
	if stateParam == "" || strings.Contains(stateParam, "evil.example.com") {
		t.Fatalf("expected signed state without evil url, got %q", stateParam)
	}

	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthStateCookieName {
			stateCookie = c
		}
	}
	if stateCookie == nil || !stateCookie.HttpOnly {
		t.Fatalf("expected HttpOnly %s cookie", oauthStateCookieName)
	}

	// 2. Callback without state cookie or with forged state must fail with ?error=invalid_state on "/"
	cbReq := httptest.NewRequest(http.MethodGet, "/api/auth/oauth/github/callback?code=123&state=https://evil.example.com", nil)
	cbRec := httptest.NewRecorder()
	srv.ServeHTTP(cbRec, cbReq)
	if cbRec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 on invalid state, got %d", cbRec.Code)
	}
	if gotLoc := cbRec.Header().Get("Location"); gotLoc != "/?error=invalid_state" {
		t.Fatalf("expected redirect to /?error=invalid_state, got %q", gotLoc)
	}

	// 3. Prevent account takeover if a different GitHub ID claims an already-bound identity
	origID, err := srv.upsertOAuthUser("github", "1001", "github:ompugao", "papa@example.com", "パパ", "")
	if err != nil || origID == "" {
		t.Fatalf("expected initial upsert to succeed, got %v", err)
	}
	if _, err := srv.upsertOAuthUser("github", "9999", "github:ompugao", "impersonator@example.com", "偽物", ""); err == nil {
		t.Fatalf("expected error when a different github_id claims an existing bound identity")
	}
}
