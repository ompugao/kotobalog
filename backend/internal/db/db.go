package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(dbPath string) (*sql.DB, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if dbPath == ":memory:" {
		database.SetMaxOpenConns(1)
		if _, err := database.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
			return nil, err
		}
	}

	if err := migrate(database); err != nil {
		database.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return database, nil
}

func migrate(database *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL DEFAULT '',
		display_name TEXT NOT NULL,
		avatar_url TEXT NOT NULL DEFAULT '',
		google_id TEXT UNIQUE,
		github_id TEXT UNIQUE,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS notebooks (
		id TEXT PRIMARY KEY,
		child_name TEXT NOT NULL,
		birth_date TEXT NOT NULL,
		theme_color TEXT NOT NULL DEFAULT '#FF8A65',
		icon_emoji TEXT NOT NULL DEFAULT '👶',
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS notebook_members (
		notebook_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'member',
		joined_at DATETIME NOT NULL,
		PRIMARY KEY (notebook_id, user_id),
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS notebook_invites (
		code TEXT PRIMARY KEY,
		notebook_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS word_entries (
		id TEXT PRIMARY KEY,
		notebook_id TEXT NOT NULL,
		created_by TEXT NOT NULL,
		word TEXT NOT NULL,
		meaning TEXT NOT NULL DEFAULT '',
		audio_path TEXT NOT NULL DEFAULT '',
		audio_mime TEXT NOT NULL DEFAULT '',
		audio_duration_ms INTEGER NOT NULL DEFAULT 0,
		spoken_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY (notebook_id) REFERENCES notebooks(id) ON DELETE CASCADE,
		FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_word_entries_notebook_spoken ON word_entries(notebook_id, spoken_at DESC);
	`
	_, err := database.Exec(schema)
	return err
}
