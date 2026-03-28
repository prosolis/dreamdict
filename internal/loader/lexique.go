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

type LexiqueLoader struct{}

func (LexiqueLoader) Name() string { return "lexique" }

func (LexiqueLoader) Load(db *sql.DB, dataDir string) error {
	path := filepath.Join(dataDir, "Lexique383.tsv")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("lexique: open: %w", err)
	}
	defer f.Close()

	posMap := map[string]string{
		"NOM": "noun",
		"VER": "verb",
		"ADJ": "adjective",
		"ADV": "adverb",
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("lexique: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE words SET pos = ?, frequency = ?
		WHERE word = ? AND lang = 'fr' AND pos IS NULL`)
	if err != nil {
		return fmt.Errorf("lexique: prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	// Skip header
	if scanner.Scan() {
		// discard header line
	}

	var count int
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 9 {
			continue
		}

		word := strings.ToLower(fields[0])
		cgram := fields[3]
		freqStr := fields[8]

		pos, ok := posMap[cgram]
		if !ok {
			continue
		}

		freq := 0.0
		if f, err := strconv.ParseFloat(freqStr, 64); err == nil {
			freq = f
		}
		freqInt := int(freq * 1000)

		if _, err := stmt.Exec(pos, freqInt, word); err != nil {
			return fmt.Errorf("lexique: update: %w", err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("lexique: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lexique: commit: %w", err)
	}
	slog.Info("lexique loaded", "enriched", count)
	return nil
}
