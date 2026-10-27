package models

import "time"

type User struct {
	ID          string    `json:"id"`
	Identity    string    `json:"identity"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	CreatedAt   time.Time `json:"created_at"`
}

type NotebookMember struct {
	UserID      string    `json:"user_id"`
	Identity    string    `json:"identity"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	AvatarURL   string    `json:"avatar_url"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
}

type Notebook struct {
	ID         string           `json:"id"`
	ChildName  string           `json:"child_name"`
	BirthDate  string           `json:"birth_date"` // YYYY-MM-DD
	ThemeColor string           `json:"theme_color"`
	IconEmoji  string           `json:"icon_emoji"`
	CreatedAt  time.Time        `json:"created_at"`
	Members    []NotebookMember `json:"members"`
}

type WordEntry struct {
	ID              string     `json:"id"`
	NotebookID      string     `json:"notebook_id"`
	CreatedBy       string     `json:"created_by"`
	CreatorName     string     `json:"creator_name"`
	Word            string     `json:"word"`
	Meaning         string     `json:"meaning"`
	HasAudio        bool       `json:"has_audio"`
	AudioMime       string     `json:"audio_mime,omitempty"`
	AudioDurationMs int        `json:"audio_duration_ms"`
	SpokenAt        time.Time  `json:"spoken_at"`
	CreatedAt       time.Time  `json:"created_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}
