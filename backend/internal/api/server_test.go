package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}
