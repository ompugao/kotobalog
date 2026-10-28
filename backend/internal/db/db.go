package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const maxBackupFiles = 3

type tableCounts struct {
	users       int64
	notebooks   int64
	wordEntries int64
}

func Open(dbPath string) (*gorm.DB, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = ":memory:?_pragma=foreign_keys(1)"
	} else {
		dsn = dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}

	gormDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.New(
			log.New(os.Stderr, "\r\n", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
			},
		),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite with gorm: %w", err)
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}

	if dbPath == ":memory:" {
		sqlDB.SetMaxOpenConns(1)
	}

	if err := migrate(gormDB, sqlDB, dbPath); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return gormDB, nil
}

func migrate(gormDB *gorm.DB, sqlDB *sql.DB, dbPath string) error {
	migrator := gormDB.Migrator()
	hasExistingData := migrator.HasTable(&models.User{}) ||
		migrator.HasTable(&models.Notebook{}) ||
		migrator.HasTable(&models.WordEntry{})

	preCounts, err := getTableCounts(gormDB, migrator)
	if err != nil {
		return fmt.Errorf("read pre-migration counts: %w", err)
	}

	needsLegacyFix, err := needsLegacyV1Reconciliation(gormDB, migrator)
	if err != nil {
		return fmt.Errorf("inspect legacy schema: %w", err)
	}

	needsSchemaChange := needsLegacyFix ||
		!migrator.HasTable(&models.User{}) ||
		!migrator.HasTable(&models.Notebook{}) ||
		!migrator.HasTable(&models.WordEntry{}) ||
		!migrator.HasColumn(&models.User{}, "identity") ||
		!migrator.HasColumn(&models.User{}, "token_version") ||
		!migrator.HasColumn(&models.WordEntry{}, "deleted_at") ||
		!migrator.HasIndex(&models.User{}, "Identity") ||
		!migrator.HasIndex(&models.WordEntry{}, "DeletedAt")

	if dbPath != ":memory:" && hasExistingData && needsSchemaChange {
		backupPath, err := createBackup(sqlDB, dbPath)
		if err != nil {
			return fmt.Errorf("pre-migration backup failed: %w", err)
		}
		log.Printf("Created pre-migration database backup at %s", backupPath)
	}

	if needsLegacyFix {
		if err := reconcileLegacyV1Schema(sqlDB); err != nil {
			return fmt.Errorf("reconcile legacy v1 schema: %w", err)
		}
	}

	if err := gormDB.AutoMigrate(&models.User{}, &models.Notebook{}, &models.WordEntry{}); err != nil {
		return fmt.Errorf("gorm auto-migrate: %w", err)
	}

	postCounts, err := getTableCounts(gormDB, migrator)
	if err != nil {
		return fmt.Errorf("read post-migration counts: %w", err)
	}

	if postCounts.users < preCounts.users ||
		postCounts.notebooks < preCounts.notebooks ||
		postCounts.wordEntries < preCounts.wordEntries {
		return fmt.Errorf(
			"row count invariant violated after migration: before=%+v after=%+v",
			preCounts, postCounts,
		)
	}

	return verifyForeignKeys(sqlDB)
}

func getTableCounts(gormDB *gorm.DB, migrator gorm.Migrator) (tableCounts, error) {
	var counts tableCounts
	if migrator.HasTable(&models.User{}) {
		if err := gormDB.Raw(`SELECT COUNT(*) FROM users`).Scan(&counts.users).Error; err != nil {
			return counts, err
		}
	}
	if migrator.HasTable(&models.Notebook{}) {
		if err := gormDB.Raw(`SELECT COUNT(*) FROM notebooks`).Scan(&counts.notebooks).Error; err != nil {
			return counts, err
		}
	}
	if migrator.HasTable(&models.WordEntry{}) {
		if err := gormDB.Raw(`SELECT COUNT(*) FROM word_entries`).Scan(&counts.wordEntries).Error; err != nil {
			return counts, err
		}
	}
	return counts, nil
}

// needsLegacyV1Reconciliation detects databases created prior to GORM migration:
// - obsolete v1 tables (notebook_members, notebook_invites)
// - legacy UNIQUE constraint on users.email
// - tab characters (\t) in sqlite_master table DDL (which glebarez/sqlite's DDL parser treats as quote characters)
func needsLegacyV1Reconciliation(gormDB *gorm.DB, migrator gorm.Migrator) (bool, error) {
	if migrator.HasTable("notebook_members") || migrator.HasTable("notebook_invites") {
		return true, nil
	}
	var sqls []string
	err := gormDB.Raw(
		`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name IN ('users', 'notebooks', 'word_entries')`,
	).Scan(&sqls).Error
	if err != nil {
		return false, err
	}
	for _, createSQL := range sqls {
		if strings.Contains(createSQL, "\t") {
			return true, nil
		}
		normalized := strings.ToUpper(strings.Join(strings.Fields(createSQL), " "))
		if strings.Contains(normalized, "EMAIL TEXT UNIQUE") {
			return true, nil
		}
	}
	return false, nil
}

func reconcileLegacyV1Schema(sqlDB *sql.DB) error {
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire dedicated conn: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`)
		_ = conn.Close()
	}()

	// PRAGMA foreign_keys must be changed outside of a transaction in SQLite
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`); err != nil {
		return fmt.Errorf("disable foreign_keys: %w", err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy reconciliation tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// 1. Drop obsolete v1 tables
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS notebook_members;`); err != nil {
		return fmt.Errorf("drop notebook_members: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS notebook_invites;`); err != nil {
		return fmt.Errorf("drop notebook_invites: %w", err)
	}

	// 2. Normalize users table (ensure missing columns exist first, then rebuild without \t or email UNIQUE)
	if hasUsers, err := txHasTable(ctx, tx, "users"); err != nil {
		return err
	} else if hasUsers {
		if hasIdentity, err := txHasColumn(ctx, tx, "users", "identity"); err != nil {
			return err
		} else if !hasIdentity {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN identity text NOT NULL DEFAULT '';`); err != nil {
				return fmt.Errorf("add users.identity: %w", err)
			}
		}
		if hasTokenVer, err := txHasColumn(ctx, tx, "users", "token_version"); err != nil {
			return err
		} else if !hasTokenVer {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN token_version integer NOT NULL DEFAULT 0;`); err != nil {
				return fmt.Errorf("add users.token_version: %w", err)
			}
		}

		// Use space-only formatting (no \t) so glebarez/sqlite's parseDDL parses every column accurately
		rebuildUsersSQL := strings.Join([]string{
			"CREATE TABLE `users__new` (",
			"  `id` text,",
			"  `identity` text NOT NULL DEFAULT '',",
			"  `email` text NOT NULL DEFAULT '',",
			"  `password_hash` text NOT NULL DEFAULT '',",
			"  `display_name` text NOT NULL,",
			"  `avatar_url` text NOT NULL DEFAULT '',",
			"  `google_id` text,",
			"  `github_id` text,",
			"  `token_version` integer NOT NULL DEFAULT 0,",
			"  `created_at` datetime NOT NULL,",
			"  PRIMARY KEY (`id`)",
			");",
			"INSERT INTO `users__new` (`id`, `identity`, `email`, `password_hash`, `display_name`, `avatar_url`, `google_id`, `github_id`, `token_version`, `created_at`)",
			"SELECT `id`, `identity`, `email`, `password_hash`, `display_name`, `avatar_url`, `google_id`, `github_id`, `token_version`, `created_at` FROM `users`;",
			"DROP TABLE `users`;",
			"ALTER TABLE `users__new` RENAME TO `users`;",
		}, "\n")
		if _, err := tx.ExecContext(ctx, rebuildUsersSQL); err != nil {
			return fmt.Errorf("rebuild users table: %w", err)
		}
	}

	// 3. Normalize notebooks table (strip \t from DDL)
	if hasNotebooks, err := txHasTable(ctx, tx, "notebooks"); err != nil {
		return err
	} else if hasNotebooks {
		rebuildNotebooksSQL := strings.Join([]string{
			"CREATE TABLE `notebooks__new` (",
			"  `id` text,",
			"  `child_name` text NOT NULL,",
			"  `birth_date` text NOT NULL,",
			"  `theme_color` text NOT NULL DEFAULT '#FF8A65',",
			"  `icon_emoji` text NOT NULL DEFAULT '👶',",
			"  `created_at` datetime NOT NULL,",
			"  PRIMARY KEY (`id`)",
			");",
			"INSERT INTO `notebooks__new` (`id`, `child_name`, `birth_date`, `theme_color`, `icon_emoji`, `created_at`)",
			"SELECT `id`, `child_name`, `birth_date`, `theme_color`, `icon_emoji`, `created_at` FROM `notebooks`;",
			"DROP TABLE `notebooks`;",
			"ALTER TABLE `notebooks__new` RENAME TO `notebooks`;",
		}, "\n")
		if _, err := tx.ExecContext(ctx, rebuildNotebooksSQL); err != nil {
			return fmt.Errorf("rebuild notebooks table: %w", err)
		}
	}

	// 4. Normalize word_entries table (ensure deleted_at exists first, then rebuild without \t)
	if hasEntries, err := txHasTable(ctx, tx, "word_entries"); err != nil {
		return err
	} else if hasEntries {
		if hasDeletedAt, err := txHasColumn(ctx, tx, "word_entries", "deleted_at"); err != nil {
			return err
		} else if !hasDeletedAt {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE word_entries ADD COLUMN deleted_at datetime;`); err != nil {
				return fmt.Errorf("add word_entries.deleted_at: %w", err)
			}
		}

		rebuildEntriesSQL := strings.Join([]string{
			"CREATE TABLE `word_entries__new` (",
			"  `id` text,",
			"  `notebook_id` text NOT NULL,",
			"  `created_by` text NOT NULL,",
			"  `word` text NOT NULL,",
			"  `meaning` text NOT NULL DEFAULT '',",
			"  `audio_path` text NOT NULL DEFAULT '',",
			"  `audio_mime` text NOT NULL DEFAULT '',",
			"  `audio_duration_ms` integer NOT NULL DEFAULT 0,",
			"  `spoken_at` datetime NOT NULL,",
			"  `created_at` datetime NOT NULL,",
			"  `deleted_at` datetime,",
			"  PRIMARY KEY (`id`),",
			"  CONSTRAINT `fk_word_entries_notebook` FOREIGN KEY (`notebook_id`) REFERENCES `notebooks`(`id`) ON DELETE CASCADE,",
			"  CONSTRAINT `fk_word_entries_creator` FOREIGN KEY (`created_by`) REFERENCES `users`(`id`) ON DELETE CASCADE",
			");",
			"INSERT INTO `word_entries__new` (`id`, `notebook_id`, `created_by`, `word`, `meaning`, `audio_path`, `audio_mime`, `audio_duration_ms`, `spoken_at`, `created_at`, `deleted_at`)",
			"SELECT `id`, `notebook_id`, `created_by`, `word`, `meaning`, `audio_path`, `audio_mime`, `audio_duration_ms`, `spoken_at`, `created_at`, `deleted_at` FROM `word_entries`;",
			"DROP TABLE `word_entries`;",
			"ALTER TABLE `word_entries__new` RENAME TO `word_entries`;",
		}, "\n")
		if _, err := tx.ExecContext(ctx, rebuildEntriesSQL); err != nil {
			return fmt.Errorf("rebuild word_entries table: %w", err)
		}
	}

	if err := txVerifyForeignKeys(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy reconciliation tx: %w", err)
	}
	return nil
}

func txHasTable(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func txHasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s);`, table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func txVerifyForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check;`)
	if err != nil {
		return fmt.Errorf("foreign_key_check query: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid, fkid sql.NullInt64
		_ = rows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("foreign key violation detected in table %s referencing %s", table, parent)
	}
	return rows.Err()
}

func verifyForeignKeys(sqlDB *sql.DB) error {
	rows, err := sqlDB.Query(`PRAGMA foreign_key_check;`)
	if err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowid, fkid sql.NullInt64
		_ = rows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("foreign key violation in table %s referencing %s", table, parent)
	}
	return rows.Err()
}

func createBackup(sqlDB *sql.DB, dbPath string) (string, error) {
	timestamp := time.Now().UTC().Format("20060102-150405.000")
	backupPath := fmt.Sprintf("%s.bak-%s", dbPath, timestamp)
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := sqlDB.Exec(fmt.Sprintf("VACUUM INTO '%s';", escaped)); err != nil {
		return "", err
	}
	info, err := os.Stat(backupPath)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("verify backup file %s: %w", backupPath, err)
	}

	pruneOldBackups(dbPath)
	return backupPath, nil
}

func pruneOldBackups(dbPath string) {
	pattern := dbPath + ".bak-*"
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) <= maxBackupFiles {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-maxBackupFiles] {
		_ = os.Remove(old)
	}
}
