package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ompugao/kotobalog/backend/internal/audio"
	"github.com/ompugao/kotobalog/backend/internal/db"
	"github.com/ompugao/kotobalog/backend/internal/models"
)

func makeValidWAV(numSamples int, freq float64) ([]byte, []byte) {
	pcm := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		v := int16(math.Sin(2*math.Pi*freq*float64(i)/44100.0) * 20000)
		binary.LittleEndian.PutUint16(pcm[i*2:i*2+2], uint16(v))
	}
	dataSize := len(pcm)
	wav := make([]byte, 44+dataSize)
	copy(wav[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(wav[4:8], uint32(36+dataSize))
	copy(wav[8:12], []byte("WAVE"))
	copy(wav[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], 44100)
	binary.LittleEndian.PutUint32(wav[28:32], 44100*2)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(wav[40:44], uint32(dataSize))
	copy(wav[44:], pcm)
	return wav, pcm
}

func doMultipart(
	t *testing.T,
	srv http.Handler,
	method, path, token string,
	fields map[string]string,
	audioBytes []byte,
	audioFilename string,
	rawAudioBytes []byte,
	rawFilename string,
) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if len(audioBytes) > 0 {
		fw, err := mw.CreateFormFile("audio", audioFilename)
		if err != nil {
			t.Fatalf("create form file audio: %v", err)
		}
		if _, err := fw.Write(audioBytes); err != nil {
			t.Fatalf("write audio bytes: %v", err)
		}
	}
	if len(rawAudioBytes) > 0 {
		fw, err := mw.CreateFormFile("raw_audio", rawFilename)
		if err != nil {
			t.Fatalf("create form file raw_audio: %v", err)
		}
		if _, err := fw.Write(rawAudioBytes); err != nil {
			t.Fatalf("write raw_audio bytes: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestFLACMultipartCreateUpdateAndCorruptRejection(t *testing.T) {
	srv := setupTestServer(t, "github:ompugao")
	uid, err := srv.upsertOAuthUser("github", "1001", "github:ompugao", "papa@example.com", "パパ", "")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	token, _ := srv.generateToken(uid, "github:ompugao")

	nbRec := doJSON(t, srv, http.MethodPost, "/api/notebooks", token, map[string]string{
		"child_name": "はるくん",
		"birth_date": "2025-05-10",
	})
	var nb models.Notebook
	_ = json.NewDecoder(nbRec.Body).Decode(&nb)

	_, trimPCM := makeValidWAV(4410, 440.0)
	_, rawPCM := makeValidWAV(22050, 330.0)
	trimFLAC, err := audio.EncodePCM16ToFLAC(trimPCM, 44100, 1)
	if err != nil {
		t.Fatalf("EncodePCM16ToFLAC trim: %v", err)
	}
	rawFLAC, err := audio.EncodePCM16ToFLAC(rawPCM, 44100, 1)
	if err != nil {
		t.Fatalf("EncodePCM16ToFLAC raw: %v", err)
	}

	// 1. Create entry via multipart/form-data with FLAC audio + raw_audio
	createRec := doMultipart(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", token,
		map[string]string{
			"word":                  "でんしゃ",
			"meaning":               "踏切を見て言った",
			"spoken_at":             "2026-11-15T10:00:00Z",
			"audio_mime":            "audio/flac",
			"audio_duration_ms":     "100",
			"raw_audio_duration_ms": "500",
			"trim_start_ms":         "100",
			"trim_end_ms":           "200",
		},
		trimFLAC, "entry.flac",
		rawFLAC, "raw_entry.flac",
	)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on FLAC multipart create, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created models.WordEntry
	_ = json.NewDecoder(createRec.Body).Decode(&created)
	if created.AudioMime != "audio/flac" || !created.HasAudio || !created.HasRawAudio {
		t.Fatalf("unexpected created entry: %+v", created)
	}

	// 2. Verify GET /audio and GET /raw-audio return exact FLAC bytes and Content-Type: audio/flac
	getAudioReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+created.ID+"/audio", nil)
	getAudioReq.Header.Set("Authorization", "Bearer "+token)
	getAudioRec := httptest.NewRecorder()
	srv.ServeHTTP(getAudioRec, getAudioReq)
	if getAudioRec.Code != http.StatusOK || !bytes.Equal(getAudioRec.Body.Bytes(), trimFLAC) {
		t.Fatalf("expected 200 and exact trimFLAC bytes, got %d", getAudioRec.Code)
	}
	if ct := getAudioRec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "audio/flac") {
		t.Fatalf("expected Content-Type audio/flac, got %q", ct)
	}

	getRawReq := httptest.NewRequest(http.MethodGet, "/api/notebooks/"+nb.ID+"/entries/"+created.ID+"/raw-audio", nil)
	getRawReq.Header.Set("Authorization", "Bearer "+token)
	getRawRec := httptest.NewRecorder()
	srv.ServeHTTP(getRawRec, getRawReq)
	if getRawRec.Code != http.StatusOK || !bytes.Equal(getRawRec.Body.Bytes(), rawFLAC) {
		t.Fatalf("expected 200 and exact rawFLAC bytes, got %d", getRawRec.Code)
	}
	if ct := getRawRec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "audio/flac") {
		t.Fatalf("expected Content-Type audio/flac on raw-audio, got %q", ct)
	}

	// 3. Update entry via multipart/form-data (re-trim without re-sending raw_audio)
	_, newTrimPCM := makeValidWAV(8820, 550.0)
	newTrimFLAC, _ := audio.EncodePCM16ToFLAC(newTrimPCM, 44100, 1)
	updateRec := doMultipart(t, srv, http.MethodPut, "/api/notebooks/"+nb.ID+"/entries/"+created.ID, token,
		map[string]string{
			"word":              "でんしゃ！",
			"meaning":           "メモ更新",
			"audio_mime":        "audio/flac",
			"audio_duration_ms": "200",
			"trim_start_ms":     "50",
			"trim_end_ms":       "250",
		},
		newTrimFLAC, "entry.flac",
		nil, "",
	)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on FLAC multipart update, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	// Verify updated audio and preserved raw_audio
	getAudioRec2 := httptest.NewRecorder()
	srv.ServeHTTP(getAudioRec2, getAudioReq)
	if !bytes.Equal(getAudioRec2.Body.Bytes(), newTrimFLAC) {
		t.Fatalf("expected updated trimFLAC bytes")
	}
	getRawRec2 := httptest.NewRecorder()
	srv.ServeHTTP(getRawRec2, getRawReq)
	if !bytes.Equal(getRawRec2.Body.Bytes(), rawFLAC) {
		t.Fatalf("expected rawFLAC to be preserved after re-trim")
	}

	// 4. Reject corrupted FLAC (valid magic bytes "fLaC", corrupted MD5/frame)
	corruptFLAC := append([]byte(nil), trimFLAC...)
	corruptFLAC[30] ^= 0xFF
	badRec := doMultipart(t, srv, http.MethodPost, "/api/notebooks/"+nb.ID+"/entries", token,
		map[string]string{"word": "壊れた音声"},
		corruptFLAC, "bad.flac",
		nil, "",
	)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on corrupted FLAC upload, got %d: %s", badRec.Code, badRec.Body.String())
	}
}

func TestMigrateWAVToFLAC(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "kotobalog.db")
	audioDir := filepath.Join(tmpDir, "audio")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	srv, err := NewServer(database, Config{
		JWTSecret: "test-secret-at-least-32-chars-long!",
		Allowlist: "github:ompugao",
		AudioDir:  audioDir,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	uid, _ := srv.upsertOAuthUser("github", "1001", "github:ompugao", "papa@example.com", "パパ", "")
	nbID := "nb-mig-1"
	_ = database.Create(&models.Notebook{
		ID:        nbID,
		ChildName: "テスト",
		BirthDate: "2025-01-01",
	}).Error

	nbDir := filepath.Join(audioDir, nbID)
	_ = os.MkdirAll(nbDir, 0755)

	wav1, pcm1 := makeValidWAV(8820, 440.0)
	rawWav1, rawPcm1 := makeValidWAV(44100, 220.0)
	_ = os.WriteFile(filepath.Join(nbDir, "e1.wav"), wav1, 0644)
	_ = os.WriteFile(filepath.Join(nbDir, "e1_raw.wav"), rawWav1, 0644)

	_ = database.Create(&models.WordEntry{
		ID:                 "e1",
		NotebookID:         nbID,
		CreatedBy:          uid,
		Word:               "わんわん",
		AudioPath:          filepath.Join(nbID, "e1.wav"),
		RawAudioPath:       filepath.Join(nbID, "e1_raw.wav"),
		AudioMime:          "audio/wav",
		AudioDurationMs:    200,
		RawAudioDurationMs: 1000,
	}).Error

	// 1. Dry-run should report 1 entry / 2 files and leave DB + files untouched
	dryStats, err := MigrateWAVToFLAC(database, dbPath, audioDir, true)
	if err != nil {
		t.Fatalf("dry-run MigrateWAVToFLAC failed: %v", err)
	}
	if dryStats.EntriesMigrated != 1 || dryStats.FilesConverted != 2 {
		t.Fatalf("unexpected dry-run stats: %+v", dryStats)
	}
	if _, err := os.Stat(filepath.Join(nbDir, "e1.flac")); !os.IsNotExist(err) {
		t.Fatalf("dry-run must not write .flac files")
	}

	// 2. Real migration run
	stats, err := MigrateWAVToFLAC(database, dbPath, audioDir, false)
	if err != nil {
		t.Fatalf("MigrateWAVToFLAC failed: %v", err)
	}
	if stats.EntriesMigrated != 1 || stats.FilesConverted != 2 || stats.DBBackupPath == "" {
		t.Fatalf("unexpected migration stats: %+v", stats)
	}
	if _, err := os.Stat(stats.DBBackupPath); err != nil {
		t.Fatalf("expected DB backup at %s: %v", stats.DBBackupPath, err)
	}

	// Verify DB record updated to .flac and audio/flac
	var migrated models.WordEntry
	if err := database.Where("id = ?", "e1").First(&migrated).Error; err != nil {
		t.Fatalf("query migrated e1: %v", err)
	}
	if migrated.AudioMime != "audio/flac" ||
		migrated.AudioPath != filepath.Join(nbID, "e1.flac") ||
		migrated.RawAudioPath != filepath.Join(nbID, "e1_raw.flac") {
		t.Fatalf("unexpected migrated DB fields: %+v", migrated)
	}

	// Verify .flac files decode bit-exactly to original PCM
	flac1Bytes, err := os.ReadFile(filepath.Join(nbDir, "e1.flac"))
	if err != nil {
		t.Fatalf("read e1.flac: %v", err)
	}
	dec1, _, err := audio.DecodeFLACToPCM16(flac1Bytes)
	if err != nil || !bytes.Equal(dec1, pcm1) {
		t.Fatalf("e1.flac bit-exact check failed: err=%v", err)
	}

	rawFlac1Bytes, err := os.ReadFile(filepath.Join(nbDir, "e1_raw.flac"))
	if err != nil {
		t.Fatalf("read e1_raw.flac: %v", err)
	}
	decRaw1, _, err := audio.DecodeFLACToPCM16(rawFlac1Bytes)
	if err != nil || !bytes.Equal(decRaw1, rawPcm1) {
		t.Fatalf("e1_raw.flac bit-exact check failed: err=%v", err)
	}

	// Verify original .wav files were moved to audio_wav_backup/
	if _, err := os.Stat(filepath.Join(nbDir, "e1.wav")); !os.IsNotExist(err) {
		t.Fatalf("expected original e1.wav to be moved out of audioDir")
	}
	if _, err := os.Stat(filepath.Join(stats.WAVBackupDir, nbID, "e1.wav")); err != nil {
		t.Fatalf("expected e1.wav in backup dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stats.WAVBackupDir, nbID, "e1_raw.wav")); err != nil {
		t.Fatalf("expected e1_raw.wav in backup dir: %v", err)
	}

	// 3. Second run must be a no-op (idempotent)
	stats2, err := MigrateWAVToFLAC(database, dbPath, audioDir, false)
	if err != nil {
		t.Fatalf("second MigrateWAVToFLAC failed: %v", err)
	}
	if stats2.EntriesScanned != 0 || stats2.EntriesMigrated != 0 {
		t.Fatalf("expected 0 scanned/migrated on second run, got: %+v", stats2)
	}
	_ = io.Discard
}
