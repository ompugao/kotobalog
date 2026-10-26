package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
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

type joinNotebookRequest struct {
	Code string `json:"code"`
}

func (s *Server) isNotebookMember(notebookID, userID string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM notebook_members WHERE notebook_id = ? AND user_id = ?`,
		notebookID, userID,
	).Scan(&count)
	return count > 0, err
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

	rows, err := s.db.Query(`
		SELECT u.id, u.display_name, u.email, u.avatar_url, nm.role, nm.joined_at
		FROM notebook_members nm
		JOIN users u ON u.id = nm.user_id
		WHERE nm.notebook_id = ?
		ORDER BY nm.joined_at ASC
	`, notebookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nb.Members = []models.NotebookMember{}
	for rows.Next() {
		var m models.NotebookMember
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Email, &m.AvatarURL, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		nb.Members = append(nb.Members, m)
	}
	return &nb, nil
}

func (s *Server) handleListNotebooks(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	rows, err := s.db.Query(`
		SELECT n.id
		FROM notebooks n
		JOIN notebook_members nm ON nm.notebook_id = n.id
		WHERE nm.user_id = ?
		ORDER BY n.created_at ASC
	`, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳一覧の取得に失敗しました")
		return
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}

	notebooks := make([]models.Notebook, 0, len(ids))
	for _, id := range ids {
		if nb, err := s.getNotebookByID(id); err == nil {
			notebooks = append(notebooks, *nb)
		}
	}
	writeJSON(w, http.StatusOK, notebooks)
}

func (s *Server) handleCreateNotebook(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
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

	tx, err := s.db.Begin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "トランザクション開始に失敗しました")
		return
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO notebooks (id, child_name, birth_date, theme_color, icon_emoji, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		notebookID, req.ChildName, req.BirthDate, req.ThemeColor, req.IconEmoji, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳の作成に失敗しました")
		return
	}

	_, err = tx.Exec(
		`INSERT INTO notebook_members (notebook_id, user_id, role, joined_at) VALUES (?, ?, 'owner', ?)`,
		notebookID, uid, now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "メンバー登録に失敗しました")
		return
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "コミットに失敗しました")
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
	uid := currentUserID(r)
	notebookID := r.PathValue("id")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

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

	_, err = s.db.Exec(
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

func generateInviteCode(length int) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	res := make([]byte, length)
	for i := 0; i < length; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		res[i] = chars[n.Int64()]
	}
	return string(res)
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	notebookID := r.PathValue("id")

	ok, err := s.isNotebookMember(notebookID, uid)
	if err != nil || !ok {
		writeError(w, http.StatusForbidden, "このことば帳へのアクセス権限がありません")
		return
	}

	code := generateInviteCode(8)
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour)

	_, err = s.db.Exec(
		`INSERT INTO notebook_invites (code, notebook_id, created_by, expires_at) VALUES (?, ?, ?, ?)`,
		code, notebookID, uid, expiresAt,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "招待コードの作成に失敗しました")
		return
	}

	writeJSON(w, http.StatusCreated, models.NotebookInvite{
		Code:       code,
		NotebookID: notebookID,
		CreatedBy:  uid,
		ExpiresAt:  expiresAt,
	})
}

func (s *Server) handleJoinNotebook(w http.ResponseWriter, r *http.Request) {
	uid := currentUserID(r)
	var req joinNotebookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "リクエスト形式が不正です")
		return
	}

	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code == "" {
		writeError(w, http.StatusBadRequest, "招待コードを入力してください")
		return
	}

	var notebookID string
	var expiresAt time.Time
	err := s.db.QueryRow(
		`SELECT notebook_id, expires_at FROM notebook_invites WHERE code = ?`,
		code,
	).Scan(&notebookID, &expiresAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "招待コードが見つかりません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "招待コードの確認に失敗しました")
		return
	}

	if time.Now().UTC().After(expiresAt) {
		writeError(w, http.StatusBadRequest, "この招待コードは有効期限が切れています")
		return
	}

	_, err = s.db.Exec(
		`INSERT OR IGNORE INTO notebook_members (notebook_id, user_id, role, joined_at) VALUES (?, ?, 'member', ?)`,
		notebookID, uid, time.Now().UTC(),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳への参加に失敗しました")
		return
	}

	nb, err := s.getNotebookByID(notebookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳情報の取得に失敗しました")
		return
	}
	writeJSON(w, http.StatusOK, nb)
}
