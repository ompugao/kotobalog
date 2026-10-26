package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/models"
)

type entryJSONRequest struct {
	Word            string `json:"word"`
	Meaning         string `json:"meaning"`
	SpokenAt        string `json:"spoken_at"`
	AudioBase64     string `json:"audio_base64"`
	AudioMime       string `json:"audio_mime"`
	AudioDurationMs int    `json:"audio_duration_ms"`
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

	query := `
		SELECT e.id, e.notebook_id, e.created_by, COALESCE(u.display_name, '家族'),
		       e.word, e.meaning, e.audio_path, e.audio_mime, e.audio_duration_ms,
		       e.spoken_at, e.created_at
		FROM word_entries e
		LEFT JOIN users u ON u.id = e.created_by
		WHERE e.notebook_id = ?
	`
	args := []any{notebookID}

	if q != "" {
		query += ` AND (e.word LIKE ? OR e.meaning LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like)
	}

	if month != "" {
		query += ` AND strftime('%Y-%m', e.spoken_at) = ?`
		args = append(args, month)
	}

	query += ` ORDER BY e.spoken_at DESC, e.created_at DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば一覧の取得に失敗しました")
		return
	}
	defer rows.Close()

	entries := []models.WordEntry{}
	for rows.Next() {
		var e models.WordEntry
		var audioPath string
		if err := rows.Scan(
			&e.ID, &e.NotebookID, &e.CreatedBy, &e.CreatorName,
			&e.Word, &e.Meaning, &audioPath, &e.AudioMime, &e.AudioDurationMs,
			&e.SpokenAt, &e.CreatedAt,
		); err != nil {
			continue
		}
		e.HasAudio = audioPath != ""
		entries = append(entries, e)
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

func (s *Server) handleCreateEntry(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	var word, meaning, audioMime string
	var spokenAt time.Time
	var audioBytes []byte
	var audioDurationMs int

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "マルチパートデータの解析に失敗しました")
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
			audioBytes, _ = io.ReadAll(file)
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
			writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
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

	_, err = s.db.Exec(`
		INSERT INTO word_entries (id, notebook_id, created_by, word, meaning, audio_path, audio_mime, audio_duration_ms, spoken_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entryID, notebookID, uid, word, meaning, audioPath, audioMime, audioDurationMs, spokenAt, now)
	if err != nil {
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
	var audioPath string
	err := s.db.QueryRow(`
		SELECT e.id, e.notebook_id, e.created_by, COALESCE(u.display_name, '家族'),
		       e.word, e.meaning, e.audio_path, e.audio_mime, e.audio_duration_ms,
		       e.spoken_at, e.created_at
		FROM word_entries e
		LEFT JOIN users u ON u.id = e.created_by
		WHERE e.notebook_id = ? AND e.id = ?
	`, notebookID, entryID).Scan(
		&e.ID, &e.NotebookID, &e.CreatedBy, &e.CreatorName,
		&e.Word, &e.Meaning, &audioPath, &e.AudioMime, &e.AudioDurationMs,
		&e.SpokenAt, &e.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	e.HasAudio = audioPath != ""
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
	spokenAt := parseSpokenAt(req.SpokenAt)

	_, err = s.db.Exec(`
		UPDATE word_entries SET word = ?, meaning = ?, spoken_at = ?
		WHERE notebook_id = ? AND id = ?
	`, word, meaning, spokenAt, notebookID, entryID)
	if err != nil {
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

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	var audioPath string
	err = s.db.QueryRow(`SELECT audio_path FROM word_entries WHERE notebook_id = ? AND id = ?`, notebookID, entryID).Scan(&audioPath)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "ことばが見つかりません")
		return
	}

	_, err = s.db.Exec(`DELETE FROM word_entries WHERE notebook_id = ? AND id = ?`, notebookID, entryID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "削除に失敗しました")
		return
	}

	if audioPath != "" {
		_ = os.Remove(filepath.Join(s.cfg.AudioDir, audioPath))
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
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

	var audioPath, audioMime string
	err = s.db.QueryRow(
		`SELECT audio_path, audio_mime FROM word_entries WHERE notebook_id = ? AND id = ?`,
		notebookID, entryID,
	).Scan(&audioPath, &audioMime)
	if err == sql.ErrNoRows || audioPath == "" {
		writeError(w, http.StatusNotFound, "音声データが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "音声データの取得に失敗しました")
		return
	}

	fullPath := filepath.Join(s.cfg.AudioDir, filepath.Clean(audioPath))
	safeMime := sanitizeAudioMime(audioMime)
	w.Header().Set("Content-Type", safeMime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeFile(w, r, fullPath)
}
