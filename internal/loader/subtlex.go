package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SubtlexLoader loads SUBTLEX-US frequency data for English words.
// Source: subtitle frequency corpus (Brysbaert & New, 2009).
// File: SUBTLEX-US.tsv (tab-separated, exported from the original XLSX).
type SubtlexLoader struct{}

func (SubtlexLoader) Name() string { return "subtlex" }

func (SubtlexLoader) Load(db *sql.DB, dataDir string) error {
	candidates := []string{
		filepath.Join(dataDir, "SUBTLEX-US.tsv"),
		filepath.Join(dataDir, "subtlex-us.tsv"),
		filepath.Join(dataDir, "SUBTLEX-US.txt"),
		filepath.Join(dataDir, "SUBTLEX-US.csv"),
		filepath.Join(dataDir, "SUBTLEXus74286wordstextversion.tsv"),
	}

	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	// Last resort: glob for anything with "subtlex" in the name
	if path == "" {
		matches, _ := filepath.Glob(filepath.Join(dataDir, "*[Ss][Uu][Bb][Tt][Ll][Ee][Xx]*"))
		if len(matches) > 0 {
			path = matches[0]
		}
	}
	if path == "" {
		slog.Warn("subtlex: no data file found, skipping", "searched", candidates)
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("subtlex: open: %w", err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("subtlex: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE words SET frequency = ? WHERE word = ? AND lang = 'en' AND frequency = 0`)
	if err != nil {
		return fmt.Errorf("subtlex: prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	// Skip header
	if scanner.Scan() {
		// Determine column layout from header
	}

	var count int
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}

		word := strings.ToLower(strings.TrimSpace(fields[0]))
		if isJunkWord(word) {
			continue
		}

		// Frequency per million — try common column positions
		// SUBTLEX-US format varies, but frequency per million is typically col 5 or 6
		var freqPerMillion float64
		for _, idx := range []int{5, 4, 3, 1} {
			if idx < len(fields) {
				if f, err := strconv.ParseFloat(strings.TrimSpace(fields[idx]), 64); err == nil && f > 0 {
					freqPerMillion = f
					break
				}
			}
		}
		if freqPerMillion <= 0 {
			continue
		}

		// Convert to integer score (multiply by 100, cap at 10000)
		freq := int(freqPerMillion * 100)
		if freq > 10000 {
			freq = 10000
		}
		if freq <= 0 {
			freq = 1
		}

		res, err := stmt.Exec(freq, word)
		if err != nil {
			return fmt.Errorf("subtlex: update: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("subtlex: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("subtlex: commit: %w", err)
	}
	slog.Info("subtlex loaded", "updated_words", count)
	return nil
}
