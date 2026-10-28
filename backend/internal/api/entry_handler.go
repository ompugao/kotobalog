package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"gorm.io/gorm"
)

type entryJSONRequest struct {
	Word            string `json:"word"`
	Meaning         string `json:"meaning"`
	SpokenAt        string `json:"spoken_at"`
	AudioBase64     string `json:"audio_base64"`
	AudioMime       string `json:"audio_mime"`
	AudioDurationMs int    `json:"audio_duration_ms"`
	RemoveAudio     bool   `json:"remove_audio"`
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

const (
	maxAudioBytes     = 20 << 20 // 20 MB
	maxEntryBodyBytes = 28 << 20 // 28 MB (allows base64 overhead for 20 MB audio)
	maxMetaBodyBytes  = 64 << 10 // 64 KB
)

func (s *Server) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBodyBytes)

	var word, meaning, audioMime string
	var spokenAt time.Time
	var audioBytes []byte
	var audioDurationMs int

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(maxAudioBytes); err != nil {
			writeError(w, http.StatusBadRequest, "マルチパートデータの解析に失敗しました（サイズ上限超過または形式不正）")
			return
		}
		word = strings.TrimSpace(r.FormValue("word"))
		meaning = strings.TrimSpace(r.FormValue("meaning"))
		spokenAt = parseSpokenAt(r.FormValue("spoken_at"))
		audioDurationMs, _ = strconv.Atoi(r.FormValue("audio_duration_ms"))
		audioMime = r.FormValue("audio_mime")

		file, header, err := r.FormFile("audio")
		if err == nil {
			defer file.Close()
			data, readErr := io.ReadAll(io.LimitReader(file, maxAudioBytes+1))
			if readErr != nil || len(data) > maxAudioBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "音声ファイルのサイズが大きすぎます（上限20MB）")
				return
			}
			audioBytes = data
			if audioMime == "" {
				audioMime = header.Header.Get("Content-Type")
			}
			if audioMime == "" {
				ext := strings.ToLower(filepath.Ext(header.Filename))
				switch ext {
				case ".wav":
					audioMime = "audio/wav"
				case ".m4a":
					audioMime = "audio/mp4"
				case ".webm":
					audioMime = "audio/webm"
				default:
					audioMime = "audio/wav"
				}
			}
		}
	} else {
		var req entryJSONRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "リクエスト形式が不正です（またはサイズ上限超過）")
			return
		}
		word = strings.TrimSpace(req.Word)
		meaning = strings.TrimSpace(req.Meaning)
		spokenAt = parseSpokenAt(req.SpokenAt)
		audioDurationMs = req.AudioDurationMs
		audioMime = req.AudioMime
		if req.AudioBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(req.AudioBase64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "音声データのデコードに失敗しました")
				return
			}
			if len(decoded) > maxAudioBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "音声ファイルのサイズが大きすぎます（上限20MB）")
				return
			}
			audioBytes = decoded
			if audioMime == "" {
				audioMime = "audio/wav"
			}
		}
	}

	if word == "" {
		writeError(w, http.StatusBadRequest, "「ことば」を入力してください")
		return
	}

	entryID := uuid.NewString()
	now := time.Now().UTC()
	audioPath := ""

	if len(audioBytes) > 0 {
		audioMime = sanitizeAudioMime(audioMime)
		safeNbID := filepath.Base(notebookID)
		nbDir := filepath.Join(s.cfg.AudioDir, safeNbID)
		if err := os.MkdirAll(nbDir, 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "音声保存ディレクトリの作成に失敗しました")
			return
		}
		filename := entryID + extForMime(audioMime)
		fullPath := filepath.Join(nbDir, filename)
		if err := os.WriteFile(fullPath, audioBytes, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "音声ファイルの保存に失敗しました")
			return
		}
		audioPath = filepath.Join(safeNbID, filename)
	} else {
		audioMime = ""
	}

	newEntry := models.WordEntry{
		ID:              entryID,
		NotebookID:      notebookID,
		CreatedBy:       uid,
		Word:            word,
		Meaning:         meaning,
		AudioPath:       audioPath,
		AudioMime:       audioMime,
		AudioDurationMs: audioDurationMs,
		SpokenAt:        spokenAt,
		CreatedAt:       now,
	}
	if err := s.db.Create(&newEntry).Error; err != nil {
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
	return &e, nil
}

func (s *Server) handleUpdateEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")
	entryID := r.PathValue("entryId")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxEntryBodyBytes)

	var req entryJSONRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	word := strings.TrimSpace(req.Word)
	meaning := strings.TrimSpace(req.Meaning)
	if word == "" {
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
	if strings.TrimSpace(req.SpokenAt) != "" {
		spokenAt = parseSpokenAt(req.SpokenAt)
	}

	updates := map[string]any{
		"word":      word,
		"meaning":   meaning,
		"spoken_at": spokenAt,
	}

	if req.AudioBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.AudioBase64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "音声データのデコードに失敗しました")
			return
		}
		if len(decoded) > maxAudioBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "音声ファイルのサイズが大きすぎます（上限20MB）")
			return
		}
		audioMime := sanitizeAudioMime(req.AudioMime)
		safeNbID := filepath.Base(notebookID)
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
		if err := os.WriteFile(fullPath, decoded, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "音声ファイルの保存に失敗しました")
			return
		}
		updates["audio_path"] = filepath.Join(safeNbID, filename)
		updates["audio_mime"] = audioMime
		updates["audio_duration_ms"] = req.AudioDurationMs
	} else if req.RemoveAudio {
		if existing.AudioPath != "" {
			_ = os.Remove(filepath.Join(s.cfg.AudioDir, existing.AudioPath))
		}
		updates["audio_path"] = ""
		updates["audio_mime"] = ""
		updates["audio_duration_ms"] = 0
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

	fullPath := filepath.Join(s.cfg.AudioDir, filepath.Clean(existing.AudioPath))
	safeMime := sanitizeAudioMime(existing.AudioMime)
	w.Header().Set("Content-Type", safeMime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, fullPath)
}
