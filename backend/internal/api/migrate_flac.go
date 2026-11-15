package api

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ompugao/kotobalog/backend/internal/audio"
	"github.com/ompugao/kotobalog/backend/internal/models"
	"gorm.io/gorm"
)

// MigrateFLACStats holds summary statistics for a WAV-to-FLAC migration run.
type MigrateFLACStats struct {
	DryRun          bool
	EntriesScanned  int
	EntriesMigrated int
	EntriesSkipped  int
	FilesConverted  int
	TotalWAVBytes   int64
	TotalFLACBytes  int64
	DBBackupPath    string
	WAVBackupDir    string
}

// MigrateWAVToFLAC converts existing .wav audio and raw_audio files to .flac
// with bit-exact verification, atomic file writes, conditional DB updates,
// and automatic backup of both the SQLite database and original WAV files.
func MigrateWAVToFLAC(database *gorm.DB, dbPath, audioDir string, dryRun bool) (*MigrateFLACStats, error) {
	wavBackupDir := filepath.Join(filepath.Dir(filepath.Clean(audioDir)), "audio_wav_backup")
	stats := &MigrateFLACStats{
		DryRun:       dryRun,
		WAVBackupDir: wavBackupDir,
	}

	var entries []models.WordEntry
	err := database.
		Where("LOWER(audio_path) LIKE '%.wav' OR LOWER(raw_audio_path) LIKE '%.wav'").
		Order("created_at ASC").
		Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("query wav entries: %w", err)
	}

	stats.EntriesScanned = len(entries)
	if len(entries) == 0 {
		log.Printf("[migrate-flac] no .wav entries found (nothing to migrate)")
		return stats, nil
	}

	if !dryRun && dbPath != "" && dbPath != ":memory:" {
		sqlDB, err := database.DB()
		if err != nil {
			return nil, fmt.Errorf("get sql.DB for backup: %w", err)
		}
		ts := time.Now().UTC().Format("20060102-150405.000")
		backupPath := fmt.Sprintf("%s.bak-preflac-%s", dbPath, ts)
		escaped := strings.ReplaceAll(backupPath, "'", "''")
		if _, err := sqlDB.Exec(fmt.Sprintf("VACUUM INTO '%s';", escaped)); err != nil {
			return nil, fmt.Errorf("create pre-flac db backup at %s: %w", backupPath, err)
		}
		info, err := os.Stat(backupPath)
		if err != nil || info.Size() == 0 {
			return nil, fmt.Errorf("verify pre-flac db backup %s: %w", backupPath, err)
		}
		stats.DBBackupPath = backupPath
		log.Printf("[migrate-flac] created DB backup: %s (%d bytes)", backupPath, info.Size())
	}

	resolveSafe := func(baseDir, relPath string) (string, error) {
		cleanedBase := filepath.Clean(baseDir)
		full := filepath.Clean(filepath.Join(cleanedBase, relPath))
		rel, err := filepath.Rel(cleanedBase, full)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe relative path: %q", relPath)
		}
		return full, nil
	}

	convertFile := func(relPath string) (newRelPath string, wavBytesLen, flacBytesLen int64, converted bool, err error) {
		if relPath == "" || !strings.HasSuffix(strings.ToLower(relPath), ".wav") {
			return relPath, 0, 0, false, nil
		}
		srcFull, err := resolveSafe(audioDir, relPath)
		if err != nil {
			return "", 0, 0, false, err
		}
		wavData, err := os.ReadFile(srcFull)
		if err != nil {
			return "", 0, 0, false, fmt.Errorf("read %s: %w", srcFull, err)
		}
		flacData, err := audio.ConvertWAVToFLACVerified(wavData)
		if err != nil {
			return "", 0, 0, false, fmt.Errorf("convert %s: %w", relPath, err)
		}

		ext := filepath.Ext(relPath)
		dstRel := strings.TrimSuffix(relPath, ext) + ".flac"
		dstFull, err := resolveSafe(audioDir, dstRel)
		if err != nil {
			return "", 0, 0, false, err
		}

		if !dryRun {
			if err := writeFileAtomicSync(dstFull, flacData, 0644); err != nil {
				return "", 0, 0, false, fmt.Errorf("write %s: %w", dstFull, err)
			}
		}

		return dstRel, int64(len(wavData)), int64(len(flacData)), true, nil
	}

	moveWAVToBackup := func(relPath string) error {
		srcFull, err := resolveSafe(audioDir, relPath)
		if err != nil {
			return err
		}
		bakFull, err := resolveSafe(wavBackupDir, relPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(bakFull), 0755); err != nil {
			return err
		}
		return os.Rename(srcFull, bakFull)
	}

	for _, entry := range entries {
		newAudioPath, wav1, flac1, conv1, err1 := convertFile(entry.AudioPath)
		if err1 != nil {
			log.Printf("[migrate-flac] SKIP entry %s (%q) audio_path=%s: %v", entry.ID, entry.Word, entry.AudioPath, err1)
			stats.EntriesSkipped++
			continue
		}

		newRawAudioPath, wav2, flac2, conv2, err2 := convertFile(entry.RawAudioPath)
		if err2 != nil {
			if !dryRun && conv1 {
				if dst1, err := resolveSafe(audioDir, newAudioPath); err == nil {
					_ = os.Remove(dst1)
				}
			}
			log.Printf("[migrate-flac] SKIP entry %s (%q) raw_audio_path=%s: %v", entry.ID, entry.Word, entry.RawAudioPath, err2)
			stats.EntriesSkipped++
			continue
		}

		if !conv1 && !conv2 {
			stats.EntriesSkipped++
			continue
		}

		if dryRun {
			stats.EntriesMigrated++
			if conv1 {
				stats.FilesConverted++
				stats.TotalWAVBytes += wav1
				stats.TotalFLACBytes += flac1
			}
			if conv2 {
				stats.FilesConverted++
				stats.TotalWAVBytes += wav2
				stats.TotalFLACBytes += flac2
			}
			continue
		}

		newMime := entry.AudioMime
		if strings.HasSuffix(strings.ToLower(newAudioPath), ".flac") {
			newMime = "audio/flac"
		}

		res := database.Model(&models.WordEntry{}).
			Where("id = ? AND audio_path = ? AND raw_audio_path = ?", entry.ID, entry.AudioPath, entry.RawAudioPath).
			Updates(map[string]any{
				"audio_path":     newAudioPath,
				"raw_audio_path": newRawAudioPath,
				"audio_mime":     newMime,
			})
		if res.Error != nil || res.RowsAffected == 0 {
			if conv1 {
				if dst1, err := resolveSafe(audioDir, newAudioPath); err == nil {
					_ = os.Remove(dst1)
				}
			}
			if conv2 {
				if dst2, err := resolveSafe(audioDir, newRawAudioPath); err == nil {
					_ = os.Remove(dst2)
				}
			}
			log.Printf("[migrate-flac] SKIP entry %s (%q): concurrent modification or DB error (err=%v, rows=%d)",
				entry.ID, entry.Word, res.Error, res.RowsAffected)
			stats.EntriesSkipped++
			continue
		}

		if conv1 {
			if err := moveWAVToBackup(entry.AudioPath); err != nil {
				log.Printf("[migrate-flac] WARN entry %s: failed to move %s to backup: %v", entry.ID, entry.AudioPath, err)
			}
			stats.FilesConverted++
			stats.TotalWAVBytes += wav1
			stats.TotalFLACBytes += flac1
		}
		if conv2 && entry.RawAudioPath != entry.AudioPath {
			if err := moveWAVToBackup(entry.RawAudioPath); err != nil {
				log.Printf("[migrate-flac] WARN entry %s: failed to move %s to backup: %v", entry.ID, entry.RawAudioPath, err)
			}
			stats.FilesConverted++
			stats.TotalWAVBytes += wav2
			stats.TotalFLACBytes += flac2
		}

		stats.EntriesMigrated++
	}

	ratio := 0.0
	if stats.TotalWAVBytes > 0 {
		ratio = float64(stats.TotalFLACBytes) * 100.0 / float64(stats.TotalWAVBytes)
	}
	log.Printf("[migrate-flac] done (dryRun=%v): scanned=%d migrated=%d skipped=%d files=%d wav=%.2fMB flac=%.2fMB (%.1f%%)",
		dryRun, stats.EntriesScanned, stats.EntriesMigrated, stats.EntriesSkipped, stats.FilesConverted,
		float64(stats.TotalWAVBytes)/(1024*1024), float64(stats.TotalFLACBytes)/(1024*1024), ratio)

	return stats, nil
}

func writeFileAtomicSync(dstPath string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	tmpPath := dstPath + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dstPath)
}
