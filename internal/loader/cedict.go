package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// CEDICTLoader imports words and definitions from the CC-CEDICT dictionary.
// CC-CEDICT line format: Traditional Simplified [pin1 yin1] /def1/def2/
type CEDICTLoader struct{}

func (CEDICTLoader) Name() string { return "cedict" }

func (CEDICTLoader) Load(db *sql.DB, dataDir string) error {
	path := dataDir + "/cedict_ts.u8"
	return loadCEDICT(db, path)
}

var cedictPOSPrefixes = map[string]string{
	"to ":  "verb",
	"(tw)": "",
}

func loadCEDICT(db *sql.DB, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cedict: open: %w", err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("cedict: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtWord, err := tx.Prepare("INSERT OR IGNORE INTO words (word, lang) VALUES (?, 'zh')")
	if err != nil {
		return fmt.Errorf("cedict: prepare word: %w", err)
	}
	defer stmtWord.Close()

	stmtDef, err := tx.Prepare(`INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority)
		SELECT id, ?, ?, 'cedict', 15 FROM words WHERE word = ? AND lang = 'zh'`)
	if err != nil {
		return fmt.Errorf("cedict: prepare def: %w", err)
	}
	defer stmtDef.Close()

	stmtTrans, err := tx.Prepare(`INSERT OR IGNORE INTO translations (word_id, translation, target_lang, source)
		SELECT id, ?, 'en', 'cedict' FROM words WHERE word = ? AND lang = 'zh'`)
	if err != nil {
		return fmt.Errorf("cedict: prepare trans: %w", err)
	}
	defer stmtTrans.Close()

	scanner := bufio.NewScanner(f)
	var wordCount, defCount, transCount int

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Format: Traditional Simplified [pinyin] /def1/def2/
		bracketStart := strings.Index(line, "[")
		slashStart := strings.Index(line, "/")
		if bracketStart < 0 || slashStart < 0 {
			continue
		}

		parts := strings.SplitN(line[:bracketStart], " ", 3)
		if len(parts) < 2 {
			continue
		}
		simplified := parts[1]

		if simplified == "" || containsDigit(simplified) {
			continue
		}

		// Insert the simplified form as the word
		if _, err := stmtWord.Exec(simplified); err != nil {
			return fmt.Errorf("cedict: insert word: %w", err)
		}
		wordCount++

		// Parse definitions between slashes
		defPart := strings.TrimRight(line[slashStart:], "/")
		defs := strings.Split(defPart, "/")
		for _, d := range defs {
			d = strings.TrimSpace(d)
			if d == "" || len(d) > 500 {
				continue
			}

			// Guess POS from definition text
			pos := ""
			lower := strings.ToLower(d)
			if strings.HasPrefix(lower, "to ") {
				pos = "verb"
			}

			// Store as definition
			if _, err := stmtDef.Exec(pos, d, simplified); err != nil {
				return fmt.Errorf("cedict: insert def: %w", err)
			}
			defCount++

			// English definitions also serve as translations
			if _, err := stmtTrans.Exec(strings.ToLower(d), simplified); err != nil {
				return fmt.Errorf("cedict: insert trans: %w", err)
			}
			transCount++
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("cedict: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cedict: commit: %w", err)
	}

	slog.Info("cedict loaded", "words", wordCount, "definitions", defCount, "translations", transCount)
	return nil
}
