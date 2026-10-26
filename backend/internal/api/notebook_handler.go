package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ompugao/kotobalog/backend/internal/models"
)

type createNotebookRequest struct {
	ChildName  string `json:"child_name"`
	BirthDate  string `json:"birth_date"`
	ThemeColor string `json:"theme_color"`
	IconEmoji  string `json:"icon_emoji"`
}

func (s *Server) isNotebookMember(notebookID, userID string) (bool, error) {
	// With ALLOWLIST mode, any authenticated user who is in ALLOWLIST can access all notebooks.
	var count int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM notebooks WHERE id = ?`, notebookID).Scan(&count)
	return count > 0, err
}

func (s *Server) listAllowlistedMembers() ([]models.NotebookMember, error) {
	rows, err := s.db.Query(`
		SELECT id, identity, display_name, email, avatar_url, created_at
		FROM users
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []models.NotebookMember{}
	for rows.Next() {
		var m models.NotebookMember
		if err := rows.Scan(&m.UserID, &m.Identity, &m.DisplayName, &m.Email, &m.AvatarURL, &m.JoinedAt); err != nil {
			continue
		}
		if m.Identity == "dev:local" || s.allow.Allows(m.Identity) {
			m.Role = "family"
			members = append(members, m)
		}
	}
	return members, nil
}

func (s *Server) getNotebookByID(notebookID string) (*models.Notebook, error) {
	var nb models.Notebook
	err := s.db.QueryRow(
		`SELECT id, child_name, birth_date, theme_color, icon_emoji, created_at FROM notebooks WHERE id = ?`,
		notebookID,
	).Scan(&nb.ID, &nb.ChildName, &nb.BirthDate, &nb.ThemeColor, &nb.IconEmoji, &nb.CreatedAt)
	if err != nil {
		return nil, err
	}

	members, err := s.listAllowlistedMembers()
	if err != nil {
		nb.Members = []models.NotebookMember{}
	} else {
		nb.Members = members
	}
	return &nb, nil
}

func (s *Server) handleListNotebooks(w http.ResponseWriter, r *http.Request) {
	members, _ := s.listAllowlistedMembers()
	if members == nil {
		members = []models.NotebookMember{}
	}

	rows, err := s.db.Query(`
		SELECT id, child_name, birth_date, theme_color, icon_emoji, created_at
		FROM notebooks
		ORDER BY created_at ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳一覧の取得に失敗しました")
		return
	}
	defer rows.Close()

	notebooks := []models.Notebook{}
	for rows.Next() {
		var nb models.Notebook
		if err := rows.Scan(&nb.ID, &nb.ChildName, &nb.BirthDate, &nb.ThemeColor, &nb.IconEmoji, &nb.CreatedAt); err == nil {
			nb.Members = members
			notebooks = append(notebooks, nb)
		}
	}
	writeJSON(w, http.StatusOK, notebooks)
}

func (s *Server) handleCreateNotebook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req createNotebookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	req.ChildName = strings.TrimSpace(req.ChildName)
	req.BirthDate = strings.TrimSpace(req.BirthDate)
	if req.ChildName == "" || req.BirthDate == "" {
		writeError(w, http.StatusBadRequest, "お子さまの名前と生年月日を入力してください")
		return
	}
	if req.ThemeColor == "" {
		req.ThemeColor = "#FF8A65"
	}
	if req.IconEmoji == "" {
		req.IconEmoji = "👶"
	}

	now := time.Now().UTC()
	notebookID := uuid.NewString()

	_, err := s.db.Exec(
		`INSERT INTO notebooks (id, child_name, birth_date, theme_color, icon_emoji, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		notebookID, req.ChildName, req.BirthDate, req.ThemeColor, req.IconEmoji, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳の作成に失敗しました")
		return
	}

	nb, err := s.getNotebookByID(notebookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "作成したことば帳の取得に失敗しました")
		return
	}
	writeJSON(w, http.StatusCreated, nb)
}

func (s *Server) handleUpdateNotebook(w http.ResponseWriter, r *http.Request) {
	notebookID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)

	var req createNotebookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	req.ChildName = strings.TrimSpace(req.ChildName)
	req.BirthDate = strings.TrimSpace(req.BirthDate)
	if req.ChildName == "" || req.BirthDate == "" {
		writeError(w, http.StatusBadRequest, "お子さまの名前と生年月日を入力してください")
		return
	}
	if req.ThemeColor == "" {
		req.ThemeColor = "#FF8A65"
	}
	if req.IconEmoji == "" {
		req.IconEmoji = "👶"
	}

	_, err := s.db.Exec(
		`UPDATE notebooks SET child_name = ?, birth_date = ?, theme_color = ?, icon_emoji = ? WHERE id = ?`,
		req.ChildName, req.BirthDate, req.ThemeColor, req.IconEmoji, notebookID,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳の更新に失敗しました")
		return
	}

	nb, err := s.getNotebookByID(notebookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "更新後のことば帳取得に失敗しました")
		return
	}
	writeJSON(w, http.StatusOK, nb)
}
