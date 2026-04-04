package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type HunspellLoader struct {
	Lang     string // "fr" or "pt-PT"
	FileName string // e.g. "fr_FR/fr.dic" or "pt_PT/pt_PT.dic"
}

func (h HunspellLoader) Name() string { return "hunspell-" + h.Lang }

func (h HunspellLoader) Load(db *sql.DB, dataDir string) error {
	path := dataDir + "/" + h.FileName
	return loadHunspell(db, path, h.Lang)
}

func loadHunspell(db *sql.DB, path, lang string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hunspell: open %s: %w", path, err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("hunspell: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO words (word, lang) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("hunspell: prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)

	// Skip first line (word count)
	if scanner.Scan() {
		// discard
	}

	var count int
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Split on "/" to get base word
		word := strings.SplitN(line, "/", 2)[0]
		word = strings.ToLower(word)

		if isJunkWord(word) {
			continue
		}

		if _, err := stmt.Exec(word, lang); err != nil {
			return fmt.Errorf("hunspell: insert: %w", err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("hunspell: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("hunspell: commit: %w", err)
	}
	slog.Info("hunspell loaded", "lang", lang, "words", count)
	return nil
}
