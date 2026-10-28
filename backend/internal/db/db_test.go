package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ompugao/kotobalog/backend/internal/models"
)

func TestMigrateExactNASSchema(t *testing.T) {
	tmpDir := t.TempDir()
	nasDBPath := filepath.Join(tmpDir, "kotobalog.db")

	rawDB, err := sql.Open("sqlite", nasDBPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}

	// Exact schema found on nas.local:/volume1/docker/kotobalog/data/kotobalog.db
	nasSchema := `
	CREATE TABLE users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL,
		avatar_url TEXT NOT NULL DEFAULT '',
		google_id TEXT UNIQUE,
		github_id TEXT UNIQUE,
		created_at DATETIME NOT NULL
	, identity TEXT NOT NULL DEFAULT '', token_version INTEGER NOT NULL DEFAULT 0);
	CREATE TABLE notebooks (
		id TEXT PRIMARY KEY,
		child_name TEXT NOT NULL,
		birth_date TEXT NOT NULL,
		theme_color TEXT NOT NULL DEFAULT '#FF8A65',
		icon_emoji TEXT NOT NULL DEFAULT '👶',
		created_at DATETIME NOT NULL
	);
	CREATE TABLE notebook_members (
		notebook_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'member',
		joined_at DATETIME NOT NULL,
		PRIMARY KEY (notebook_id, user_id),
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE TABLE notebook_invites (
		code TEXT PRIMARY KEY,
		notebook_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE TABLE word_entries (
		id TEXT PRIMARY KEY,
		notebook_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		word TEXT NOT NULL,
		meaning TEXT NOT NULL DEFAULT '',
		audio_path TEXT NOT NULL DEFAULT '',
		audio_mime TEXT NOT NULL DEFAULT '',
		audio_duration_ms INTEGER NOT NULL DEFAULT 0,
		spoken_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL, deleted_at DATETIME,
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE
	);
	CREATE INDEX idx_word_entries_notebook_spoken ON word_entries(notebook_id, spoken_at DESC);

	INSERT INTO users (id, email, display_name, github_id, identity, token_version, created_at)
	VALUES ('u1', 'papa@example.com', 'パパ', '1001', 'github:ompugao', 1, '2026-10-26 22:27:00.000000000 +0000 UTC');
	INSERT INTO users (id, email, display_name, google_id, identity, token_version, created_at)
	VALUES ('u2', 'mama@example.com', 'ママ', '2002', 'google:mama@example.com', 0, '2026-10-26 22:27:10.000000000 +0000 UTC');

	INSERT INTO notebooks (id, child_name, birth_date, created_at)
	VALUES ('nb1', 'お子さま', '2025-01-01', '2026-10-26 22:27:32.304343058 +0000 UTC');

	INSERT INTO notebook_members (notebook_id, user_id, role, joined_at)
	VALUES ('nb1', 'u1', 'owner', '2026-10-26 22:27:32.304343058 +0000 UTC');

	INSERT INTO word_entries (id, notebook_id, created_by, word, meaning, spoken_at, created_at)
	VALUES ('e1', 'nb1', 'u1', 'まんま', 'ごはんのこと', '2026-10-26 22:47:59.053825 +0000 UTC', '2026-10-26 22:48:43.940000107 +0000 UTC');
	INSERT INTO word_entries (id, notebook_id, created_by, word, meaning, spoken_at, created_at)
	VALUES ('e2', 'nb1', 'u2', 'わんわん', 'いぬ', '2026-10-28 08:57:12.102841 +0000 UTC', '2026-10-28 08:57:15.000000000 +0000 UTC');
	`
	if _, err := rawDB.Exec(nasSchema); err != nil {
		t.Fatalf("exec nas schema: %v", err)
	}
	_ = rawDB.Close()

	// 1. Open with db.Open to trigger backup + migration
	gormDB, err := Open(nasDBPath)
	if err != nil {
		t.Fatalf("Open on NAS schema failed: %v", err)
	}

	// Verify backup file was created
	backups, err := filepath.Glob(nasDBPath + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected 1 backup file, got %v (err=%v)", backups, err)
	}

	// Verify obsolete tables were dropped
	if gormDB.Migrator().HasTable("notebook_members") || gormDB.Migrator().HasTable("notebook_invites") {
		t.Fatalf("expected obsolete v1 tables notebook_members and notebook_invites to be dropped")
	}

	// Verify all users, notebooks, and word_entries are preserved and readable via GORM
	var users []models.User
	if err := gormDB.Order("created_at ASC").Find(&users).Error; err != nil || len(users) != 2 {
		t.Fatalf("expected 2 users, got %d (err=%v)", len(users), err)
	}
	if users[0].Identity != "github:ompugao" || users[0].TokenVersion != 1 {
		t.Fatalf("unexpected user[0]: %+v", users[0])
	}

	var entries []models.WordEntry
	if err := gormDB.Where("deleted_at IS NULL").Order("spoken_at DESC").Find(&entries).Error; err != nil || len(entries) != 2 {
		t.Fatalf("expected 2 active entries, got %d (err=%v)", len(entries), err)
	}
	if entries[0].Word != "わんわん" || entries[1].Word != "まんま" {
		t.Fatalf("unexpected entries order/content: %+v", entries)
	}

	// Verify legacy UNIQUE constraint on users.email was removed (can insert another user with empty/same email)
	dupEmailUser := models.User{
		ID:          "u3",
		Identity:    "dev:local",
		Email:       "papa@example.com",
		DisplayName: "テスト",
	}
	if err := gormDB.Create(&dupEmailUser).Error; err != nil {
		t.Fatalf("expected non-unique email after migration, got error: %v", err)
	}

	sqlDB, _ := gormDB.DB()
	_ = sqlDB.Close()

	// 2. Re-open already migrated DB: should be idempotent and NOT create a second backup
	gormDB2, err := Open(nasDBPath)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer func() {
		if sdb, err := gormDB2.DB(); err == nil {
			_ = sdb.Close()
		}
	}()

	backupsAfterSecondOpen, _ := filepath.Glob(nasDBPath + ".bak-*")
	if len(backupsAfterSecondOpen) != 1 {
		t.Fatalf("expected no additional backup on already-migrated DB, got %d backups", len(backupsAfterSecondOpen))
	}
}

func TestMigrateExistingLegacyDB(t *testing.T) {
	tmpDir := t.TempDir()
	legacyDBPath := filepath.Join(tmpDir, "legacy.db")

	// 1. Create a v1 legacy database schema (without identity, token_version, or deleted_at)
	rawDB, err := sql.Open("sqlite", legacyDBPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	legacySchema := `
	CREATE TABLE users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL,
		avatar_url TEXT NOT NULL DEFAULT '',
		google_id TEXT UNIQUE,
		github_id TEXT UNIQUE,
		created_at DATETIME NOT NULL
	);
	CREATE TABLE notebooks (
		id TEXT PRIMARY KEY,
		child_name TEXT NOT NULL,
		birth_date TEXT NOT NULL,
		theme_color TEXT NOT NULL DEFAULT '#FF8A65',
		icon_emoji TEXT NOT NULL DEFAULT '👶',
		created_at DATETIME NOT NULL
	);
	CREATE TABLE word_entries (
		id TEXT PRIMARY KEY,
		notebook_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		word TEXT NOT NULL,
		meaning TEXT NOT NULL DEFAULT '',
		audio_path TEXT NOT NULL DEFAULT '',
		audio_mime TEXT NOT NULL DEFAULT '',
		audio_duration_ms INTEGER NOT NULL DEFAULT 0,
		spoken_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL
	);
	INSERT INTO users (id, email, display_name, created_at) VALUES ('u1', 'papa@example.com', 'パパ', '2026-10-26T00:00:00Z');
	INSERT INTO notebooks (id, child_name, birth_date, created_at) VALUES ('nb1', 'お子さま', '2025-01-01', '2026-10-26T00:00:00Z');
	INSERT INTO word_entries (id, notebook_id, created_by, word, spoken_at, created_at) VALUES ('e1', 'nb1', 'u1', 'まんま', '2026-10-26T00:00:00Z', '2026-10-26T00:00:00Z');
	`
	if _, err := rawDB.Exec(legacySchema); err != nil {
		t.Fatalf("exec legacy schema: %v", err)
	}
	_ = rawDB.Close()

	// 2. Open with db.Open to trigger migrate() on the legacy DB
	migratedDB, err := Open(legacyDBPath)
	if err != nil {
		t.Fatalf("Open on legacy DB failed: %v", err)
	}
	defer func() {
		if sqlDB, err := migratedDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	// 3. Verify deleted_at column exists and existing row is preserved with deleted_at IS NULL
	var count int64
	if err := migratedDB.Model(&models.WordEntry{}).Where("deleted_at IS NULL").Count(&count).Error; err != nil {
		t.Fatalf("query deleted_at on migrated DB failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active entry after migration, got %d", count)
	}
}
