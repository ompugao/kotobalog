package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateExistingLegacyDB(t *testing.T) {
	tmpDir := t.TempDir()
	legacyDBPath := filepath.Join(tmpDir, "legacy.db")

	// 1. Create a legacy database schema (without deleted_at on word_entries)
	rawDB, err := sql.Open("sqlite", legacyDBPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	legacySchema := `
	CREATE TABLE users (
		id TEXT PRIMARY KEY,
		identity TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL DEFAULT '',
		password_hash TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL,
		avatar_url TEXT NOT NULL DEFAULT '',
		google_id TEXT,
		github_id TEXT,
		token_version INTEGER NOT NULL DEFAULT 0,
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
	INSERT INTO users (id, identity, display_name, created_at) VALUES ('u1', 'github:test', 'パパ', '2026-10-26T00:00:00Z');
	INSERT INTO notebooks (id, child_name, birth_date, created_at) VALUES ('nb1', 'お子さま', '2025-01-01', '2026-10-26T00:00:00Z');
	INSERT INTO word_entries (id, notebook_id, created_by, word, spoken_at, created_at) VALUES ('e1', 'nb1', 'u1', 'まんま', '2026-10-26T00:00:00Z', '2026-10-26T00:00:00Z');
	`
	if _, err := rawDB.Exec(legacySchema); err != nil {
		t.Fatalf("exec legacy schema: %v", err)
	}
	rawDB.Close()

	// 2. Open with db.Open to trigger migrate() on the legacy DB
	migratedDB, err := Open(legacyDBPath)
	if err != nil {
		t.Fatalf("Open on legacy DB failed: %v", err)
	}
	defer migratedDB.Close()

	// 3. Verify deleted_at column exists and existing row is preserved with deleted_at IS NULL
	var count int
	if err := migratedDB.QueryRow(`SELECT COUNT(*) FROM word_entries WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("query deleted_at on migrated DB failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 active entry after migration, got %d", count)
	}
}
