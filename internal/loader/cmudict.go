package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// CMUDictLoader loads pronunciation data from the CMU Pronouncing Dictionary.
// Format: WORD  P1 P2 P3 (two-space separated word and phonemes)
// Coverage: English only (~134,000 entries).
type CMUDictLoader struct{}

func (CMUDictLoader) Name() string { return "cmudict" }

func (CMUDictLoader) Load(db *sql.DB, dataDir string) error {
	candidates := []string{
		filepath.Join(dataDir, "cmudict-0.7b"),
		filepath.Join(dataDir, "cmudict.dict"),
		filepath.Join(dataDir, "cmudict"),
	}

	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		slog.Warn("cmudict: no data file found, skipping", "searched", candidates)
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cmudict: open: %w", err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("cmudict: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO pronunciations (word_id, format, value, source)
		SELECT id, 'cmu', ?, 'cmudict' FROM words WHERE word = ? AND lang = 'en'`)
	if err != nil {
		return fmt.Errorf("cmudict: prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	var count int

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ";;;") {
			continue
		}

		// Format: "WORD  PH1 PH2 PH3" (two spaces between word and phonemes)
		// Some entries have variant markers like "WORD(2)  PH1 PH2"
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			// Try single space (some versions)
			idx := strings.IndexByte(line, ' ')
			if idx == -1 {
				continue
			}
			parts = []string{line[:idx], strings.TrimSpace(line[idx+1:])}
		}

		word := strings.ToLower(strings.TrimSpace(parts[0]))
		phonemes := strings.TrimSpace(parts[1])

		if word == "" || phonemes == "" {
			continue
		}

		// Skip variant entries like "WORD(2)" — keep only primary pronunciation
		if strings.Contains(word, "(") {
			continue
		}

		if isJunkWord(word) {
			continue
		}

		res, err := stmt.Exec(phonemes, word)
		if err != nil {
			return fmt.Errorf("cmudict: insert: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("cmudict: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cmudict: commit: %w", err)
	}
	slog.Info("cmudict loaded", "pronunciations", count)
	return nil
}
