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

type SCOWLLoader struct{}

func (SCOWLLoader) Name() string { return "scowl" }

func (SCOWLLoader) Load(db *sql.DB, dataDir string) error {
	pattern := filepath.Join(dataDir, "scowl", "final", "english-words.*")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("scowl: glob: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("scowl: no files matching %s", pattern)
	}

	// Filter to sizes <= 70
	var selected []string
	for _, f := range files {
		base := filepath.Base(f)
		// Files are like english-words.10, english-words.20, etc.
		parts := strings.SplitN(base, ".", 2)
		if len(parts) != 2 {
			continue
		}
		var size int
		if _, err := fmt.Sscanf(parts[1], "%d", &size); err != nil {
			continue
		}
		if size <= 70 {
			selected = append(selected, f)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("scowl: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("INSERT OR IGNORE INTO words (word, lang) VALUES (?, 'en')")
	if err != nil {
		return fmt.Errorf("scowl: prepare: %w", err)
	}
	defer stmt.Close()

	var count int
	for _, f := range selected {
		n, err := loadSCOWLFile(stmt, f)
		if err != nil {
			return fmt.Errorf("scowl: load %s: %w", f, err)
		}
		count += n
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scowl: commit: %w", err)
	}
	slog.Info("scowl loaded", "words", count, "files", len(selected))
	return nil
}

func loadSCOWLFile(stmt *sql.Stmt, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		word := strings.ToLower(line)
		if containsAny(word, " ", "'", "-") || containsDigit(word) {
			continue
		}
		if _, err := stmt.Exec(word); err != nil {
			return 0, err
		}
		count++
	}
	return count, scanner.Err()
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func containsSpace(s string) bool {
	return strings.ContainsRune(s, ' ')
}

func containsNonLatin(s string) bool {
	for _, r := range s {
		if r > 0x024F && r != '\'' && r != '-' {
			return true
		}
	}
	return false
}
