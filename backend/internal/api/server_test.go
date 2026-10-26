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

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	srv, err := NewServer(database, Config{
		JWTSecret: "test-secret",
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

func TestNotebookAndWordEntryFlow(t *testing.T) {
	srv := setupTestServer(t)

	// 1. Register Mama
	rec := doJSON(t, srv, http.MethodPost, "/api/auth/register", "", map[string]string{
		"email":        "mama@example.com",
		"password":     "password123",
		"display_name": "ママ",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on register mama, got %d: %s", rec.Code, rec.Body.String())
	}
	var mamaAuth authResponse
	_ = json.NewDecoder(rec.Body).Decode(&mamaAuth)

	// 2. Register Papa
	rec = doJSON(t, srv, http.MethodPost, "/api/auth/register", "", map[string]string{
		"email":        "papa@example.com",
		"password":     "password123",
		"display_name": "パパ",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on register papa, got %d", rec.Code)
	}
	var papaAuth authResponse
	_ = json.NewDecoder(rec.Body).Decode(&papaAuth)

	// 3. Mama creates a notebook ("はるくんのことば帳")
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks", mamaAuth.Token, map[string]string{
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
	if nb.ChildName != "はるくん" || len(nb.Members) != 1 {
		t.Fatalf("unexpected notebook: %+v", nb)
	}

	// 4. Mama creates an invite code and Papa joins
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/invites", mamaAuth.Token, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create invite, got %d", rec.Code)
	}
	var inv models.NotebookInvite
	_ = json.NewDecoder(rec.Body).Decode(&inv)

	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/join", papaAuth.Token, map[string]string{
		"code": inv.Code,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on papa join, got %d: %s", rec.Code, rec.Body.String())
	}
	var joinedNb models.Notebook
	_ = json.NewDecoder(rec.Body).Decode(&joinedNb)
	if len(joinedNb.Members) != 2 {
		t.Fatalf("expected 2 members in notebook, got %d", len(joinedNb.Members))
	}

	// 5. Papa records a word with audio
	dummyWav := []byte("RIFFxxxxWAVEfmt ")
	rec = doJSON(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", papaAuth.Token, map[string]any{
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

	// 6. Mama searches entries and fetches audio
	rec = doJSON(t, srv, http.MethodGet, "/api/notebooks/"+nb.ID+"/entries?q=救急車", mamaAuth.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list entries, got %d", rec.Code)
	}
	var entries []models.WordEntry
	_ = json.NewDecoder(rec.Body).Decode(&entries)
	if len(entries) != 1 || entries[0].Word != "ぶーぶー" {
		t.Fatalf("unexpected search results: %+v", entries)
	}

	// 7. Fetch audio using ?token= query param
	audioReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+createdEntry.ID+"/audio?token="+mamaAuth.Token, nil)
	audioRec := httptest.NewRecorder()
	srv.ServeHTTP(audioRec, audioReq)
	if audioRec.Code != http.StatusOK || !bytes.Equal(audioRec.Body.Bytes(), dummyWav) {
		t.Fatalf("expected 200 and audio bytes, got %d", audioRec.Code)
	}
}
