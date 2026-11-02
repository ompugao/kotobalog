package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ompugao/kotobalog/backend/internal/db"
	"github.com/ompugao/kotobalog/backend/internal/models"
)

func setupTestServer(t *testing.T, allowlist string) *Server {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	srv, err := NewServer(database, Config{
		JWTSecret: "test-secret-at-least-32-chars-long!",
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
	dummyRawWav := []byte("RIFFrawxxxxxWAVEfmt ")
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaToken, map[string]any{
		"word":                  "ぶーぶー",
		"meaning":               "パトカーや救急車を見つけたときに指差して言った",
		"spoken_at":             "2026-10-26T10:30:00Z",
		"audio_base64":          base64.StdEncoding.EncodeToString(dummyWav),
		"raw_audio_base64":      base64.StdEncoding.EncodeToString(dummyRawWav),
		"audio_mime":            "audio/wav",
		"audio_duration_ms":     2400,
		"raw_audio_duration_ms": 5000,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create entry, got %d: %s", rec.Code, rec.Body.String())
	}
	var createdEntry models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&createdEntry)
	if !createdEntry.HasAudio || !createdEntry.HasRawAudio || createdEntry.CreatorName != "パパ" {
		t.Fatalf("unexpected entry: %+v", createdEntry)
	}

	// 5. Mama searches entries and fetches audio and raw audio
	rec = doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries?q=救急車", mamaToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list entries, got %d", rec.Code)
	}
	var entries []models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&entries)
	if len(entries) != 1 || entries[0].Word != "ぶーぶー" || !entries[0].HasRawAudio {
		t.Fatalf("unexpected search results: %+v", entries)
	}

	// 6. Fetching audio using ?token= query param is now rejected (401) to prevent log leakage
	audioReqBad := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/audio?token="+mamaToken, nil)
	audioRecBad := httptest.NewRecorder()
	srv.ServeHTTP(audioRecBad, audioReqBad)
	if audioRecBad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on legacy ?token= parameter, got %d", audioRecBad.Code)
	}

	// 6.1 Fetch audio using standard Bearer header
	audioReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/audio", nil)
	audioReq.Header.Set("Authorization", "Bearer "+mamaToken)
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

	// 6.2 Fetch audio using short-lived scoped media ticket
	ticketRec := doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/media-ticket", mamaToken, nil)
	if ticketRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on media-ticket, got %d: %s", ticketRec.Code, ticketRec.Body.String())
	}
	var ticketResp struct {
		Ticket string `json:"ticket"`
	}
	_ = json.NewDecoder(ticketRec.Body).Decode(&ticketResp)
	if ticketResp.Ticket == "" {
		t.Fatalf("expected non-empty media ticket")
	}

	audioReqTicket := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/audio?ticket="+ticketResp.Ticket, nil)
	audioRecTicket := httptest.NewRecorder()
	srv.ServeHTTP(audioRecTicket, audioReqTicket)
	if audioRecTicket.Code != http.StatusOK || !bytes.Equal(audioRecTicket.Body.Bytes(), dummyWav) {
		t.Fatalf("expected 200 via media ticket, got %d", audioRecTicket.Code)
	}

	// 6.3 Fetch raw audio using Bearer header
	rawReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/raw-audio", nil)
	rawReq.Header.Set("Authorization", "Bearer "+mamaToken)
	rawRec := httptest.NewRecorder()
	srv.ServeHTTP(rawRec, rawReq)
	if rawRec.Code != http.StatusOK || !bytes.Equal(rawRec.Body.Bytes(), dummyRawWav) {
		t.Fatalf("expected 200 and raw audio bytes, got %d", rawRec.Code)
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

	// 6.55 Update with invalid raw audio is rejected; valid raw audio replaces the stored one
	badRaw := doJSON(t, srv, http.MethodPut, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID, mamaToken, map[string]any{
		"word":             "ぶーぶー！",
		"audio_base64":     base64.StdEncoding.EncodeToString(trimmedWav),
		"audio_mime":       "audio/wav",
		"raw_audio_base64": "!!!not-base64!!!",
	})
	if badRaw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid raw audio, got %d", badRaw.Code)
	}
	newRaw := []byte("RIFFnewrawWAVEfmt ")
	goodRaw := doJSON(t, srv, http.MethodPut, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID, mamaToken, map[string]any{
		"word":                  "ぶーぶー！",
		"audio_base64":          base64.StdEncoding.EncodeToString(trimmedWav),
		"audio_mime":            "audio/wav",
		"audio_duration_ms":     1200,
		"raw_audio_base64":      base64.StdEncoding.EncodeToString(newRaw),
		"raw_audio_duration_ms": 6000,
	})
	if goodRaw.Code != http.StatusOK {
		t.Fatalf("expected 200 on update with raw audio, got %d: %s", goodRaw.Code, goodRaw.Body.String())
	}
	rawReq2 := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/raw-audio", nil)
	rawReq2.Header.Set("Authorization", "Bearer "+mamaToken)
	rawRec2 := httptest.NewRecorder()
	srv.ServeHTTP(rawRec2, rawReq2)
	if !bytes.Equal(rawRec2.Body.Bytes(), newRaw) {
		t.Fatalf("expected updated raw audio bytes, got %q", rawRec2.Body.String())
	}

	// 6.5.2 Re-trim again without passing raw_audio_base64; existing raw audio should remain intact!
	retrimmedWav := []byte("RIFFretrimmedWAVEfmt ")
	retrimRec := doJSON(t, srv, http.MethodPut, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID, mamaToken, map[string]any{
		"word":              "ぶーぶー！",
		"audio_base64":      base64.StdEncoding.EncodeToString(retrimmedWav),
		"audio_mime":        "audio/wav",
		"audio_duration_ms": 800,
	})
	if retrimRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on re-trim, got %d: %s", retrimRec.Code, retrimRec.Body.String())
	}
	rawReq3 := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/raw-audio", nil)
	rawReq3.Header.Set("Authorization", "Bearer "+mamaToken)
	rawRec3 := httptest.NewRecorder()
	srv.ServeHTTP(rawRec3, rawReq3)
	if !bytes.Equal(rawRec3.Body.Bytes(), newRaw) {
		t.Fatalf("expected raw audio to be safely preserved after re-trimming, got %q", rawRec3.Body.String())
	}

	// 6.5.3 Legacy audio migration: create a legacy entry without raw_audio, call migrateLegacyRawAudio and verify
	legacyID := "legacy-entry-1"
	legacyDir := filepath.Join(srv.cfg.AudioDir, nb.ID)
	_ = os.MkdirAll(legacyDir, 0755)
	legacyAudioBytes := []byte("RIFFlegacyaudioWAVEfmt ")
	if err := os.WriteFile(filepath.Join(legacyDir, legacyID+".wav"), legacyAudioBytes, 0644); err != nil {
		t.Fatalf("write legacy audio: %v", err)
	}
	legacyEntry := models.WordEntry{
		ID:              legacyID,
		NotebookID:      nb.ID,
		CreatedBy:       papaID,
		Word:            "まんま",
		AudioPath:       filepath.Join(nb.ID, legacyID+".wav"),
		AudioMime:       "audio/wav",
		AudioDurationMs: 1500,
	}
	if err := srv.db.Create(&legacyEntry).Error; err != nil {
		t.Fatalf("create legacy entry: %v", err)
	}
	// Run migration
	srv.migrateLegacyRawAudio()

	var migrated models.WordEntry
	if err := srv.db.Where("id = ?", legacyID).First(&migrated).Error; err != nil {
		t.Fatalf("find migrated entry: %v", err)
	}
	if migrated.RawAudioPath == "" || migrated.RawAudioDurationMs != 1500 {
		t.Fatalf("expected migrated raw audio path and duration, got: %+v", migrated)
	}
	migratedRawBytes, err := os.ReadFile(filepath.Join(srv.cfg.AudioDir, migrated.RawAudioPath))
	if err != nil || !bytes.Equal(migratedRawBytes, legacyAudioBytes) {
		t.Fatalf("migrated raw audio file mismatch: %v", err)
	}
	// Clean up legacy test entry
	_ = srv.db.Where("id = ?", legacyID).Delete(&models.WordEntry{}).Error

	// 6.6 Soft delete (trash) and restore entry safeguard
	delRec := doJSON(t, srv, http.MethodDelete, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID, mamaToken, nil)
	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on soft delete, got %d", delRec.Code)
	}
	// Active list should not include soft-deleted entry
	activeRec := doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries", mamaToken, nil)
	var activeEntries []models.WordEntry
	_ = json.NewDecoder(activeRec.Body).Decode(&activeEntries)
	if len(activeEntries) != 0 {
		t.Fatalf("expected 0 active entries after soft delete, got %d", len(activeEntries))
	}
	// Trash list (?deleted=1) should include soft-deleted entry and preserve audio
	trashRec := doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries?deleted=1", mamaToken, nil)
	var trashedEntries []models.WordEntry
	_ = json.NewDecoder(trashRec.Body).Decode(&trashedEntries)
	if len(trashedEntries) != 1 || trashedEntries[0].DeletedAt == nil || !trashedEntries[0].HasAudio {
		t.Fatalf("expected 1 trashed entry with DeletedAt and HasAudio, got %+v", trashedEntries)
	}
	// Restore entry
	restoreRec := doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/restore", mamaToken, nil)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on restore entry, got %d", restoreRec.Code)
	}
	activeAfterRestoreRec := doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries", mamaToken, nil)
	_ = json.NewDecoder(activeAfterRestoreRec.Body).Decode(&activeEntries)
	if len(activeEntries) != 1 || activeEntries[0].DeletedAt != nil {
		t.Fatalf("expected 1 active entry after restore, got %+v", activeEntries)
	}

	// 7. Uploading invalid non-audio data (e.g. text/html with script) must be rejected with 400 Bad Request
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaToken, map[string]any{
		"word":         "てすと",
		"audio_base64": base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>")),
		"audio_mime":   "text/html",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on non-audio data, got %d", rec.Code)
	}

	// 7.1 Valid audio bytes with untrusted MIME is sanitized to audio/wav
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaToken, map[string]any{
		"word":         "てすと2",
		"audio_base64": base64.StdEncoding.EncodeToString(dummyWav),
		"audio_mime":   "text/html",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid WAV, got %d: %s", rec.Code, rec.Body.String())
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

func TestDryRunCopiedNASDatabase(t *testing.T) {
	nasDBPath := os.Getenv("NAS_DRYRUN_DB")
	if nasDBPath == "" {
		t.Skip("NAS_DRYRUN_DB not set; skipping NAS live copy dry-run")
	}

	database, err := db.Open(nasDBPath)
	if err != nil {
		t.Fatalf("db.Open on NAS copy failed: %v", err)
	}
	defer func() {
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	// Verify backup was created
	backups, err := filepath.Glob(nasDBPath + ".bak-*")
	if err != nil || len(backups) == 0 {
		t.Fatalf("expected backup file for NAS DB, got %v (err=%v)", backups, err)
	}

	var users []models.User
	if err := database.Order("created_at ASC").Find(&users).Error; err != nil {
		t.Fatalf("query users failed: %v", err)
	}
	var notebooks []models.Notebook
	if err := database.Order("created_at ASC").Find(&notebooks).Error; err != nil {
		t.Fatalf("query notebooks failed: %v", err)
	}
	var totalEntries int64
	if err := database.Model(&models.WordEntry{}).Count(&totalEntries).Error; err != nil {
		t.Fatalf("count word_entries failed: %v", err)
	}
	t.Logf("Migrated NAS DB successfully: users=%d, notebooks=%d, word_entries=%d, backup=%s",
		len(users), len(notebooks), totalEntries, backups[0])

	if len(users) < 2 || len(notebooks) < 1 || totalEntries < 58 {
		t.Fatalf("unexpected counts after NAS DB migration: users=%d notebooks=%d entries=%d", len(users), len(notebooks), totalEntries)
	}

	// Verify API endpoints against migrated NAS DB
	allowIdentities := []string{}
	for _, u := range users {
		if u.Identity != "" {
			allowIdentities = append(allowIdentities, u.Identity)
		}
	}
	srv, err := NewServer(database, Config{
		JWTSecret: "test-secret-for-nas-dryrun-verification",
		Allowlist: strings.Join(allowIdentities, ","),
		AudioDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	tok, err := srv.generateToken(users[0].ID, users[0].Identity)
	if err != nil {
		t.Fatalf("generateToken failed: %v", err)
	}

	nbRec := doJSON(t, srv, http.MethodGet, "/api/notebooks", tok, nil)
	if nbRec.Code != http.StatusOK {
		t.Fatalf("GET /api/notebooks returned %d: %s", nbRec.Code, nbRec.Body.String())
	}

	entriesRec := doJSON(t, srv, http.MethodGet, "/api/notebooks/"+notebooks[0].ID+"/entries", tok, nil)
	if entriesRec.Code != http.StatusOK {
		t.Fatalf("GET /api/notebooks/%s/entries returned %d: %s", notebooks[0].ID, entriesRec.Code, entriesRec.Body.String())
	}
	var apiEntries []models.WordEntry
	if err := json.NewDecoder(entriesRec.Body).Decode(&apiEntries); err != nil {
		t.Fatalf("decode entries failed: %v", err)
	}
	t.Logf("API returned %d active entries for notebook %q (first entry word=%q, creator=%q, spoken_at=%v)",
		len(apiEntries), notebooks[0].ChildName, apiEntries[0].Word, apiEntries[0].CreatorName, apiEntries[0].SpokenAt)
}

func TestPKCEAndPathTraversalSecurity(t *testing.T) {
	srv := setupTestServer(t, "github:alice")
	aliceID, err := srv.upsertOAuthUser("github", "777", "github:alice", "alice@example.com", "Alice", "")
	if err != nil {
		t.Fatalf("upsert alice: %v", err)
	}
	aliceToken, _ := srv.generateToken(aliceID, "github:alice")

	// 1. PKCE Code Exchange Verification
	verifier := "my-secret-pkce-verifier-string-1234567890"
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	// Mock state with PKCE challenge and kotobalog://auth custom scheme
	stateToken, _, err := srv.generateOAuthState("kotobalog://auth", challenge, "S256")
	if err != nil {
		t.Fatalf("generateOAuthState: %v", err)
	}
	_ = stateToken

	// Simulate OAuth callback producing authCode
	srv.exchangeMu.Lock()
	testCode := "test-pkce-exchange-code-12345"
	srv.exchangeCodes[testCode] = authExchangeRecord{
		userID:              aliceID,
		identity:            "github:alice",
		codeChallenge:       challenge,
		codeChallengeMethod: "S256",
		redirectURI:         "kotobalog://auth",
		expiresAt:           time.Now().Add(2 * time.Minute),
	}
	srv.exchangeMu.Unlock()

	// 1.1 Attempt exchange with wrong code_verifier -> MUST fail
	badExRec := doJSON(t, srv, http.MethodPost, "/api/auth/exchange", "", map[string]string{
		"code":          testCode,
		"code_verifier": "wrong-verifier",
	})
	if badExRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on wrong code_verifier, got %d", badExRec.Code)
	}

	// 1.2 Re-inject code and exchange with correct verifier -> MUST succeed
	srv.exchangeMu.Lock()
	srv.exchangeCodes[testCode] = authExchangeRecord{
		userID:              aliceID,
		identity:            "github:alice",
		codeChallenge:       challenge,
		codeChallengeMethod: "S256",
		redirectURI:         "kotobalog://auth",
		expiresAt:           time.Now().Add(2 * time.Minute),
	}
	srv.exchangeMu.Unlock()

	goodExRec := doJSON(t, srv, http.MethodPost, "/api/auth/exchange", "", map[string]string{
		"code":          testCode,
		"code_verifier": verifier,
	})
	if goodExRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid PKCE exchange, got %d: %s", goodExRec.Code, goodExRec.Body.String())
	}
	var exResp struct {
		Token string      `json:"token"`
		User  models.User `json:"user"`
	}
	_ = json.NewDecoder(goodExRec.Body).Decode(&exResp)
	if exResp.Token == "" || exResp.User.ID != aliceID {
		t.Fatalf("unexpected exchange response: %+v", exResp)
	}

	// 1.3 Replay attack: reusing the burned code -> MUST fail (400)
	replayRec := doJSON(t, srv, http.MethodPost, "/api/auth/exchange", "", map[string]string{
		"code":          testCode,
		"code_verifier": verifier,
	})
	if replayRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on replayed auth code, got %d", replayRec.Code)
	}

	// 2. Path Traversal Safeguards
	// 2.1 Attempt create entry with directory traversal in notebook ID
	badNbRec := doJSON(t, srv, http.MethodPost, "/api/notebooks/..%2f..%2fetc/entries", aliceToken, map[string]any{
		"word": "hacker",
	})
	if badNbRec.Code != http.StatusBadRequest && badNbRec.Code != http.StatusNotFound && badNbRec.Code != http.StatusForbidden {
		t.Fatalf("expected 400/403/404 for path traversal in notebook ID, got %d", badNbRec.Code)
	}

	// 2.2 Create legitimate entry
	nbRec := doJSON(t, srv, http.MethodPost, "/api/notebooks", aliceToken, map[string]string{
		"child_name": "テスト",
		"birth_date": "2025-01-01",
	})
	var nb models.Notebook
	_ = json.NewDecoder(nbRec.Body).Decode(&nb)

	entryRec := doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", aliceToken, map[string]any{
		"word":         "パパ",
		"audio_base64": base64.StdEncoding.EncodeToString([]byte("RIFFtestWAVEfmt ")),
		"audio_mime":   "audio/wav",
	})
	var entry models.WordEntry
	_ = json.NewDecoder(entryRec.Body).Decode(&entry)

	// 2.3 Directly tamper database audio_path to attempt relative directory traversal escape
	_ = srv.db.Model(&models.WordEntry{}).Where("id = ?", entry.ID).Update("audio_path", "../../../etc/passwd").Error

	travReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+entry.ID+"/audio", nil)
	travReq.Header.Set("Authorization", "Bearer "+aliceToken)
	travRec := httptest.NewRecorder()
	srv.ServeHTTP(travRec, travReq)
	if travRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on path traversal audio retrieval, got %d", travRec.Code)
	}
}
