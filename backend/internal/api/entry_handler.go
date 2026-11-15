package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/audio"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"gorm.io/gorm"
)

func isValidID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validateAudioMagicBytes(data []byte) (detectedMime string, ok bool) {
	if len(data) < 12 {
		return "", false
	}
	// FLAC stream: offset 0..4 is "fLaC"
	if bytes.HasPrefix(data, []byte("fLaC")) {
		return "audio/flac", true
	}
	// RIFF container with WAVE
	if bytes.HasPrefix(data, []byte("RIFF")) && (bytes.Equal(data[8:12], []byte("WAVE")) || bytes.Contains(data[:min(len(data), 64)], []byte("WAVE"))) {
		return "audio/wav", true
	}
	// OggS
	if bytes.HasPrefix(data, []byte("OggS")) {
		return "audio/ogg", true
	}
	// WebM EBML
	if bytes.HasPrefix(data, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return "audio/webm", true
	}
	// MP4 / M4A: offset 4..8 is "ftyp"
	if bytes.Equal(data[4:8], []byte("ftyp")) {
		return "audio/mp4", true
	}
	return "", false
}

type entryJSONRequest struct {
	Word               string `json:"word"`
	Meaning            string `json:"meaning"`
	SpokenAt           string `json:"spoken_at"`
	AudioBase64        string `json:"audio_base64"`
	RawAudioBase64     string `json:"raw_audio_base64"`
	AudioMime          string `json:"audio_mime"`
	AudioDurationMs    int    `json:"audio_duration_ms"`
	RawAudioDurationMs int    `json:"raw_audio_duration_ms"`
	TrimStartMs        int    `json:"trim_start_ms"`
	TrimEndMs          int    `json:"trim_end_ms"`
	RemoveAudio        bool   `json:"remove_audio"`
}

func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	month := strings.TrimSpace(r.URL.Query().Get("month")) // YYYY-MM
	onlyDeleted := r.URL.Query().Get("deleted") == "1" || r.URL.Query().Get("deleted") == "true"

	tx := s.db.Table("word_entries e").
		Select("e.*, COALESCE(u.display_name, '家族') AS creator_name").
		Joins("LEFT JOIN users u ON u.id = e.created_by").
		Where("e.notebook_id = ?", notebookID)

	if onlyDeleted {
		tx = tx.Where("e.deleted_at IS NOT NULL")
	} else {
		tx = tx.Where("e.deleted_at IS NULL")
	}

	if q != "" {
		like := "%" + q + "%"
		tx = tx.Where("(e.word LIKE ? OR e.meaning LIKE ?)", like, like)
	}

	if month != "" {
		tx = tx.Where("substr(e.spoken_at, 1, 7) = ?", month)
	}

	if onlyDeleted {
		tx = tx.Order("e.deleted_at DESC, e.spoken_at DESC")
	} else {
		tx = tx.Order("e.spoken_at DESC, e.created_at DESC")
	}

	var entries []models.WordEntry
	if err := tx.Find(&entries).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "ことば一覧の取得に失敗しました")
		return
	}
	if entries == nil {
		entries = []models.WordEntry{}
	}
	for i := range entries {
		entries[i].HasAudio = entries[i].AudioPath != ""
		entries[i].HasRawAudio = entries[i].RawAudioPath != ""
	}
	writeJSON(w, http.StatusOK, entries)
}

func parseSpokenAt(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}

func sanitizeAudioMime(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.Contains(mime, "flac"):
		return "audio/flac"
	case strings.Contains(mime, "wav"):
		return "audio/wav"
	case strings.Contains(mime, "webm"):
		return "audio/webm"
	case strings.Contains(mime, "ogg") || strings.Contains(mime, "opus"):
		return "audio/ogg"
	case strings.Contains(mime, "mp4") || strings.Contains(mime, "m4a") || strings.Contains(mime, "aac"):
		return "audio/mp4"
	default:
		return "audio/wav"
	}
}

func extForMime(mime string) string {
	switch sanitizeAudioMime(mime) {
	case "audio/flac":
		return ".flac"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4":
		return ".m4a"
	default:
		return ".wav"
	}
}

func mimeForPath(filePath, fallbackMime string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filePath), "."))
	if ext != "" {
		return sanitizeAudioMime(ext)
	}
	return sanitizeAudioMime(fallbackMime)
}

const (
	maxAudioBytes     = 20 << 20 // 20 MB
	maxEntryBodyBytes = 56 << 20 // 56 MB (allows base64 overhead for two 20 MB audio files)
	maxMetaBodyBytes  = 64 << 10 // 64 KB
)

type parsedEntryInput struct {
	word               string
	meaning            string
	spokenAtRaw        string
	audioMime          string
	audioBytes         []byte
	rawAudioBytes      []byte
	audioDurationMs    int
	rawAudioDurationMs int
	trimStartMs        int
	trimEndMs          int
	removeAudio        bool
}

func parseEntryRequest(r *http.Request) (*parsedEntryInput, int, string) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxAudioBytes); err != nil {
			return nil, http.StatusBadRequest, "マルチパートデータの解析に失敗しました（サイズ上限超過または形式不正）"
		}
		in := &parsedEntryInput{
			word:        strings.TrimSpace(r.FormValue("word")),
			meaning:     strings.TrimSpace(r.FormValue("meaning")),
			spokenAtRaw: strings.TrimSpace(r.FormValue("spoken_at")),
			audioMime:   r.FormValue("audio_mime"),
			removeAudio: r.FormValue("remove_audio") == "1" || r.FormValue("remove_audio") == "true",
		}
		in.audioDurationMs, _ = strconv.Atoi(r.FormValue("audio_duration_ms"))
		in.rawAudioDurationMs, _ = strconv.Atoi(r.FormValue("raw_audio_duration_ms"))
		in.trimStartMs, _ = strconv.Atoi(r.FormValue("trim_start_ms"))
		in.trimEndMs, _ = strconv.Atoi(r.FormValue("trim_end_ms"))

		file, header, err := r.FormFile("audio")
		if err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(io.LimitReader(file, maxAudioBytes+1))
			if readErr != nil || len(data) > maxAudioBytes {
				return nil, http.StatusRequestEntityTooLarge, "音声ファイルのサイズが大きすぎます（上限20MB）"
			}
			in.audioBytes = data
			if in.audioMime == "" {
				in.audioMime = header.Header.Get("Content-Type")
			}
			if in.audioMime == "" {
				ext := strings.ToLower(filepath.Ext(header.Filename))
				in.audioMime = sanitizeAudioMime(ext)
			}
		}

		rawFile, _, err := r.FormFile("raw_audio")
		if err == nil {
			defer rawFile.Close()
			rawData, readErr := io.ReadAll(io.LimitReader(rawFile, maxAudioBytes+1))
			if readErr != nil {
				return nil, http.StatusBadRequest, "元音声データの読み込みに失敗しました"
			}
			if len(rawData) > maxAudioBytes {
				return nil, http.StatusRequestEntityTooLarge, "元音声ファイルのサイズが大きすぎます（上限20MB）"
			}
			in.rawAudioBytes = rawData
		}
		return in, 0, ""
	}

	var req entryJSONRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, http.StatusBadRequest, "リクエスト形式が不正です（またはサイズ上限超過）"
	}
	in := &parsedEntryInput{
		word:               strings.TrimSpace(req.Word),
		meaning:            strings.TrimSpace(req.Meaning),
		spokenAtRaw:        strings.TrimSpace(req.SpokenAt),
		audioMime:          req.AudioMime,
		audioDurationMs:    req.AudioDurationMs,
		rawAudioDurationMs: req.RawAudioDurationMs,
		trimStartMs:        req.TrimStartMs,
		trimEndMs:          req.TrimEndMs,
		removeAudio:        req.RemoveAudio,
	}
	if req.AudioBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.AudioBase64)
		if err != nil {
			return nil, http.StatusBadRequest, "音声データのデコードに失敗しました"
		}
		if len(decoded) > maxAudioBytes {
			return nil, http.StatusRequestEntityTooLarge, "音声ファイルのサイズが大きすぎます（上限20MB）"
		}
		in.audioBytes = decoded
		if in.audioMime == "" {
			in.audioMime = "audio/wav"
		}
	}
	if req.RawAudioBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.RawAudioBase64)
		if err != nil {
			return nil, http.StatusBadRequest, "元音声データのデコードに失敗しました"
		}
		if len(decoded) > maxAudioBytes {
			return nil, http.StatusRequestEntityTooLarge, "元音声ファイルのサイズが大きすぎます（上限20MB）"
		}
		in.rawAudioBytes = decoded
	}
	return in, 0, ""
}

func validateAndVerifyAudioBytes(data []byte) (detectedMime string, errMsg string) {
	detectedMime, ok := validateAudioMagicBytes(data)
	if !ok {
		return "", "有効な音声ファイル（FLAC, WAV, WebM, OGG, M4A）ではありません"
	}
	if detectedMime == "audio/flac" {
		if _, err := audio.VerifyFLAC(data); err != nil {
			return "", "FLAC音声データが破損しているか形式が不正です"
		}
	}
	return detectedMime, ""
}

func (s *Server) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	if !isValidID(notebookID) {
		writeError(w, http.StatusBadRequest, "無効なことば帳IDです")
		return
	}

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBodyBytes)

	in, status, errMsg := parseEntryRequest(r)
	if status != 0 {
		writeError(w, status, errMsg)
		return
	}
	if in.word == "" {
		writeError(w, http.StatusBadRequest, "「ことば」を入力してください")
		return
	}

	spokenAt := parseSpokenAt(in.spokenAtRaw)
	entryID := uuid.NewString()
	now := time.Now().UTC()
	audioPath := ""
	rawAudioPath := ""
	audioMime := ""

	if len(in.audioBytes) > 0 {
		detectedMime, vErr := validateAndVerifyAudioBytes(in.audioBytes)
		if vErr != "" {
			writeError(w, http.StatusBadRequest, vErr)
			return
		}
		audioMime = detectedMime

		detectedRawMime := ""
		if len(in.rawAudioBytes) > 0 {
			var rErr string
			detectedRawMime, rErr = validateAndVerifyAudioBytes(in.rawAudioBytes)
			if rErr != "" {
				writeError(w, http.StatusBadRequest, rErr)
				return
			}
		}

		safeNbID := notebookID
		nbDir := filepath.Join(s.cfg.AudioDir, safeNbID)
		if err := os.MkdirAll(nbDir, 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "音声保存ディレクトリの作成に失敗しました")
			return
		}
		filename := entryID + extForMime(audioMime)
		fullPath := filepath.Join(nbDir, filename)
		if err := os.WriteFile(fullPath, in.audioBytes, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "音声ファイルの保存に失敗しました")
			return
		}
		audioPath = filepath.Join(safeNbID, filename)

		if len(in.rawAudioBytes) > 0 {
			rawFilename := entryID + "_raw" + extForMime(detectedRawMime)
			rawFullPath := filepath.Join(nbDir, rawFilename)
			if err := os.WriteFile(rawFullPath, in.rawAudioBytes, 0644); err != nil {
				_ = os.Remove(fullPath)
				writeError(w, http.StatusInternalServerError, "元音声ファイルの保存に失敗しました")
				return
			}
			rawAudioPath = filepath.Join(safeNbID, rawFilename)
		}
	}

	trimStartMs := in.trimStartMs
	trimEndMs := in.trimEndMs
	if trimEndMs == 0 && in.audioDurationMs > 0 {
		trimEndMs = trimStartMs + in.audioDurationMs
	}

	newEntry := models.WordEntry{
		ID:                 entryID,
		NotebookID:         notebookID,
		CreatedBy:          uid,
		Word:               in.word,
		Meaning:            in.meaning,
		AudioPath:          audioPath,
		RawAudioPath:       rawAudioPath,
		AudioMime:          audioMime,
		AudioDurationMs:    in.audioDurationMs,
		RawAudioDurationMs: in.rawAudioDurationMs,
		TrimStartMs:        trimStartMs,
		TrimEndMs:          trimEndMs,
		SpokenAt:           spokenAt,
		CreatedAt:          now,
	}
	if err := s.db.Create(&newEntry).Error; err != nil {
		if audioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, audioPath))
		}
		if rawAudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, rawAudioPath))
		}
		writeError(w, http.StatusInternalServerError, "ことばの保存に失敗しました")
		return
	}

	entry, err := s.getEntryByID(notebookID, entryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存したことばの取得に失敗しました")
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) getEntryByID(notebookID, entryID string) (*models.WordEntry, error) {
	var e models.WordEntry
	err := s.db.Table("word_entries e").
		Select("e.*, COALESCE(u.display_name, '家族') AS creator_name").
		Joins("LEFT JOIN users u ON u.id = e.created_by").
		Where("e.notebook_id = ? AND e.id = ?", notebookID, entryID).
		First(&e).Error
	if err != nil {
		return nil, err
	}
	e.HasAudio = e.AudioPath != ""
	e.HasRawAudio = e.RawAudioPath != ""
	return &e, nil
}

func (s *Server) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	if !isValidID(notebookID) || !isValidID(entryID) {
		writeError(w, http.StatusBadRequest, "無効なIDです")
		return
	}

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBodyBytes)

	in, status, errMsg := parseEntryRequest(r)
	if status != 0 {
		writeError(w, status, errMsg)
		return
	}
	if in.word == "" {
		writeError(w, http.StatusBadRequest, "「ことば」を入力してください")
		return
	}

	var existing models.WordEntry
	err = s.db.Where("notebook_id = ? AND id = ?", notebookID, entryID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "ことばの取得に失敗しました")
		return
	}

	spokenAt := existing.SpokenAt
	if in.spokenAtRaw != "" {
		spokenAt = parseSpokenAt(in.spokenAtRaw)
	}

	updates := map[string]any{
		"word":      in.word,
		"meaning":   in.meaning,
		"spoken_at": spokenAt,
	}

	if len(in.audioBytes) > 0 {
		detectedMime, vErr := validateAndVerifyAudioBytes(in.audioBytes)
		if vErr != "" {
			writeError(w, http.StatusBadRequest, vErr)
			return
		}
		audioMime := detectedMime

		detectedRawMime := ""
		if len(in.rawAudioBytes) > 0 {
			var rErr string
			detectedRawMime, rErr = validateAndVerifyAudioBytes(in.rawAudioBytes)
			if rErr != "" {
				writeError(w, http.StatusBadRequest, rErr)
				return
			}
		}

		safeNbID := notebookID
		nbDir := filepath.Join(s.cfg.AudioDir, safeNbID)
		if err := os.MkdirAll(nbDir, 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "音声保存ディレクトリの作成に失敗しました")
			return
		}
		filename := entryID + extForMime(audioMime)
		fullPath := filepath.Join(nbDir, filename)
		if existing.AudioPath != "" && existing.AudioPath != filepath.Join(safeNbID, filename) {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.AudioPath))
		}
		if err := os.WriteFile(fullPath, in.audioBytes, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "音声ファイルの保存に失敗しました")
			return
		}
		updates["audio_path"] = filepath.Join(safeNbID, filename)
		updates["audio_mime"] = audioMime
		updates["audio_duration_ms"] = in.audioDurationMs
		updates["trim_start_ms"] = in.trimStartMs
		if in.trimEndMs > 0 {
			updates["trim_end_ms"] = in.trimEndMs
		} else if in.audioDurationMs > 0 {
			updates["trim_end_ms"] = in.trimStartMs + in.audioDurationMs
		}

		if len(in.rawAudioBytes) > 0 {
			rawFilename := entryID + "_raw" + extForMime(detectedRawMime)
			rawFullPath := filepath.Join(nbDir, rawFilename)
			if existing.RawAudioPath != "" && existing.RawAudioPath != filepath.Join(safeNbID, rawFilename) {
				_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.RawAudioPath))
			}
			if err := os.WriteFile(rawFullPath, in.rawAudioBytes, 0644); err != nil {
				writeError(w, http.StatusInternalServerError, "元音声ファイルの保存に失敗しました")
				return
			}
			updates["raw_audio_path"] = filepath.Join(safeNbID, rawFilename)
			updates["raw_audio_duration_ms"] = in.rawAudioDurationMs
		}
	} else if in.removeAudio {
		if existing.AudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.AudioPath))
		}
		if existing.RawAudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.RawAudioPath))
		}
		updates["audio_path"] = ""
		updates["raw_audio_path"] = ""
		updates["audio_mime"] = ""
		updates["audio_duration_ms"] = 0
		updates["raw_audio_duration_ms"] = 0
		updates["trim_start_ms"] = 0
		updates["trim_end_ms"] = 0
	}

	if err := s.db.Model(&models.WordEntry{}).Where("notebook_id = ? AND id = ?", notebookID, entryID).Updates(updates).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "ことばの更新に失敗しました")
		return
	}

	entry, err := s.getEntryByID(notebookID, entryID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")
	permanent := r.URL.Query().Get("permanent") == "1" || r.URL.Query().Get("permanent") == "true"

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	var existing models.WordEntry
	err = s.db.Where("notebook_id = ? AND id = ?", notebookID, entryID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "ことばの取得に失敗しました")
		return
	}

	if permanent {
		if err := s.db.Where("notebook_id = ? AND id = ?", notebookID, entryID).Delete(&models.WordEntry{}).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "完全削除に失敗しました")
			return
		}
		if existing.AudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.AudioPath))
		}
		if existing.RawAudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.RawAudioPath))
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "permanently_deleted"})
		return
	}

	now := time.Now().UTC()
	if err := s.db.Model(&models.WordEntry{}).Where("notebook_id = ? AND id = ?", notebookID, entryID).Update("deleted_at", now).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "ゴミ箱への移動に失敗しました")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "trashed"})
}

func (s *Server) handleRestoreEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	res := s.db.Model(&models.WordEntry{}).Where("notebook_id = ? AND id = ?", notebookID, entryID).Update("deleted_at", nil)
	if res.Error != nil {
		writeError(w, http.StatusInternalServerError, "復元に失敗しました")
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	}

	entry, err := s.getEntryByID(notebookID, entryID)
	if err != nil {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleGetEntryAudio(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	if !isValidID(notebookID) || !isValidID(entryID) {
		writeError(w, http.StatusBadRequest, "無効なIDです")
		return
	}

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	var existing models.WordEntry
	err = s.db.Select("audio_path", "audio_mime").Where("notebook_id = ? AND id = ?", notebookID, entryID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || existing.AudioPath == "" {
		writeError(w, http.StatusNotFound, "音声データが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "音声データの取得に失敗しました")
		return
	}

	fullPath, err := s.safeAudioPath(existing.AudioPath)
	if err != nil {
		writeError(w, http.StatusForbidden, "無効なファイルパスです")
		return
	}
	safeMime := mimeForPath(existing.AudioPath, existing.AudioMime)
	w.Header().Set("Content-Type", safeMime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, fullPath)
}

func (s *Server) migrateLegacyRawAudio() {
	var entries []models.WordEntry
	if err := s.db.Where("audio_path != '' AND (raw_audio_path = '' OR raw_audio_path IS NULL)").Find(&entries).Error; err != nil {
		log.Printf("legacy raw audio migration: query failed: %v", err)
		return
	}

	for _, entry := range entries {
		srcPath, err := s.safeAudioPath(entry.AudioPath)
		if err != nil {
			log.Printf("legacy raw audio migration: skip entry %s: invalid path: %v", entry.ID, err)
			continue
		}
		srcBytes, err := os.ReadFile(srcPath)
		if err != nil || len(srcBytes) == 0 {
			log.Printf("legacy raw audio migration: skip entry %s: cannot read %s: %v", entry.ID, srcPath, err)
			continue
		}

		ext := filepath.Ext(entry.AudioPath)
		dir := filepath.Dir(entry.AudioPath)
		rawRelPath := filepath.Join(dir, entry.ID+"_raw"+ext)
		rawFullPath, err := s.safeAudioPath(rawRelPath)
		if err != nil {
			log.Printf("legacy raw audio migration: skip entry %s: invalid raw path: %v", entry.ID, err)
			continue
		}

		if err := os.WriteFile(rawFullPath, srcBytes, 0644); err != nil {
			log.Printf("legacy raw audio migration: entry %s: write failed: %v", entry.ID, err)
			continue
		}

		dur := entry.AudioDurationMs
		if entry.RawAudioDurationMs > 0 {
			dur = entry.RawAudioDurationMs
		}

		if err := s.db.Model(&models.WordEntry{}).Where("id = ?", entry.ID).Updates(map[string]any{
			"raw_audio_path":        rawRelPath,
			"raw_audio_duration_ms": dur,
			"trim_start_ms":         0,
			"trim_end_ms":           entry.AudioDurationMs,
		}).Error; err != nil {
			log.Printf("legacy raw audio migration: entry %s: db update failed: %v", entry.ID, err)
			_ = os.Remove(rawFullPath)
		}
	}
}

func (s *Server) handleGetEntryRawAudio(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	if !isValidID(notebookID) || !isValidID(entryID) {
		writeError(w, http.StatusBadRequest, "無効なIDです")
		return
	}

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	var existing models.WordEntry
	err = s.db.Select("raw_audio_path", "audio_path", "audio_mime").Where("notebook_id = ? AND id = ?", notebookID, entryID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, http.StatusNotFound, "音声データが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "音声データの取得に失敗しました")
		return
	}

	targetPath := existing.RawAudioPath
	// If raw_audio_path is not set but audio_path is set (e.g. before raw audio feature), fallback to audio_path
	if targetPath == "" {
		targetPath = existing.AudioPath
	}
	if targetPath == "" {
		writeError(w, http.StatusNotFound, "音声データが見つかりません")
		return
	}

	fullPath, err := s.safeAudioPath(targetPath)
	if err != nil {
		writeError(w, http.StatusForbidden, "無効なファイルパスです")
		return
	}
	safeMime := mimeForPath(targetPath, existing.AudioMime)
	w.Header().Set("Content-Type", safeMime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, fullPath)
}
