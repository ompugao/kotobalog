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
	var count int64
	err := s.db.Model(&models.Notebook{}).Where("id = ?", notebookID).Count(&count).Error
	return count > 0, err
}

func (s *Server) listAllowlistedMembers() ([]models.NotebookMember, error) {
	var users []models.User
	if err := s.db.Order("created_at ASC").Find(&users).Error; err != nil {
		return nil, err
	}

	members := []models.NotebookMember{}
	for _, u := range users {
		if u.Identity == "dev:local" || s.allow.Allows(u.Identity) {
			members = append(members, models.NotebookMember{
				UserID:      u.ID,
				Identity:    u.Identity,
				DisplayName: u.DisplayName,
				Email:       u.Email,
				AvatarURL:   u.AvatarURL,
				Role:        "family",
				JoinedAt:    u.CreatedAt,
			})
		}
	}
	return members, nil
}

func (s *Server) getNotebookByID(notebookID string) (*models.Notebook, error) {
	var nb models.Notebook
	if err := s.db.Where("id = ?", notebookID).First(&nb).Error; err != nil {
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

	var notebooks []models.Notebook
	if err := s.db.Order("created_at ASC").Find(&notebooks).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳一覧の取得に失敗しました")
		return
	}

	for i := range notebooks {
		notebooks[i].Members = members
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

	nbModel := models.Notebook{
		ID:         uuid.NewString(),
		ChildName:  req.ChildName,
		BirthDate:  req.BirthDate,
		ThemeColor: req.ThemeColor,
		IconEmoji:  req.IconEmoji,
		CreatedAt:  time.Now().UTC(),
	}

	if err := s.db.Create(&nbModel).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "ことば帳の作成に失敗しました")
		return
	}

	nb, err := s.getNotebookByID(nbModel.ID)
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

	err := s.db.Model(&models.Notebook{}).Where("id = ?", notebookID).Updates(map[string]any{
		"child_name":  req.ChildName,
		"birth_date":  req.BirthDate,
		"theme_color": req.ThemeColor,
		"icon_emoji":  req.IconEmoji,
	}).Error
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
