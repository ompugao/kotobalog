package models

import "time"

type User struct {
	ID           string    `gorm:"primaryKey;type:text" json:"id"`
	Identity     string    `gorm:"type:text;not null;default:'';index" json:"identity"`
	Email        string    `gorm:"type:text;not null;default:''" json:"email"`
	PasswordHash string    `gorm:"type:text;not null;default:''" json:"-"`
	DisplayName  string    `gorm:"type:text;not null" json:"display_name"`
	AvatarURL    string    `gorm:"type:text;not null;default:''" json:"avatar_url"`
	GoogleID     *string   `gorm:"column:google_id;type:text;index" json:"-"`
	GitHubID     *string   `gorm:"column:github_id;type:text;index" json:"-"`
	TokenVersion int       `gorm:"not null;default:0" json:"-"`
	CreatedAt    time.Time `gorm:"not null;autoCreateTime:false" json:"created_at"`
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
	ID         string           `gorm:"primaryKey;type:text" json:"id"`
	ChildName  string           `gorm:"type:text;not null" json:"child_name"`
	BirthDate  string           `gorm:"type:text;not null" json:"birth_date"` // YYYY-MM-DD
	ThemeColor string           `gorm:"type:text;not null;default:'#FF8A65'" json:"theme_color"`
	IconEmoji  string           `gorm:"type:text;not null;default:'👶'" json:"icon_emoji"`
	CreatedAt  time.Time        `gorm:"not null;autoCreateTime:false" json:"created_at"`
	Members    []NotebookMember `gorm:"-" json:"members"`
}

type WordEntry struct {
	ID              string     `gorm:"primaryKey;type:text" json:"id"`
	NotebookID      string     `gorm:"type:text;not null;index:idx_word_entries_notebook_spoken,priority:1" json:"notebook_id"`
	CreatedBy       string     `gorm:"type:text;not null" json:"created_by"`
	CreatorName     string     `gorm:"->;-:migration" json:"creator_name"`
	Word            string     `gorm:"type:text;not null" json:"word"`
	Meaning         string     `gorm:"type:text;not null;default:''" json:"meaning"`
	AudioPath       string     `gorm:"type:text;not null;default:''" json:"-"`
	HasAudio        bool       `gorm:"-" json:"has_audio"`
	AudioMime       string     `gorm:"type:text;not null;default:''" json:"audio_mime,omitempty"`
	AudioDurationMs int        `gorm:"not null;default:0" json:"audio_duration_ms"`
	SpokenAt        time.Time  `gorm:"not null;index:idx_word_entries_notebook_spoken,priority:2,sort:desc" json:"spoken_at"`
	CreatedAt       time.Time  `gorm:"not null;autoCreateTime:false" json:"created_at"`
	DeletedAt       *time.Time `gorm:"index" json:"deleted_at,omitempty"`

	Notebook *Notebook `gorm:"foreignKey:NotebookID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
	Creator  *User     `gorm:"foreignKey:CreatedBy;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}
