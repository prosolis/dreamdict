package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type SCOWLLoader struct{}

func (SCOWLLoader) Name() string { return "scowl" }

// scowlFrequency maps SCOWL tier to a frequency score.
// Higher = more common, matching the Lexique convention.
// SCOWL 10 = most common words, 70 = rare/specialized.
var scowlFrequency = map[int]int{
	10: 1000,
	20: 800,
	35: 600,
	40: 500,
	50: 400,
	55: 300,
	60: 200,
	65: 100,
	70: 50,
}

type scowlFile struct {
	path    string
	size    int
	variant string // "us", "gb", or "" for common English
}

func (SCOWLLoader) Load(db *sql.DB, dataDir string) error {
	type prefixVariant struct {
		prefix  string
		variant string
	}
	prefixes := []prefixVariant{
		{"english-words", ""},
		{"american-words", "us"},
		{"british-words", "gb"},
	}

	var allFiles []string
	for _, pv := range prefixes {
		pattern := filepath.Join(dataDir, "scowl", "final", pv.prefix+".*")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("scowl: glob: %w", err)
		}
		allFiles = append(allFiles, matches...)
	}
	if len(allFiles) == 0 {
		return fmt.Errorf("scowl: no word files found in %s", filepath.Join(dataDir, "scowl", "final"))
	}

	// Build a map from prefix to variant for quick lookup
	prefixToVariant := make(map[string]string)
	for _, pv := range prefixes {
		prefixToVariant[pv.prefix] = pv.variant
	}

	// Filter to sizes <= 70 and track the tier + variant
	var selected []scowlFile
	for _, f := range allFiles {
		base := filepath.Base(f)
		// Files are like english-words.10, american-words.35, etc.
		parts := strings.SplitN(base, ".", 2)
		if len(parts) != 2 {
			continue
		}
		prefix := parts[0]
		variant, ok := prefixToVariant[prefix]
		if !ok {
			continue
		}
		var size int
		if _, err := fmt.Sscanf(parts[1], "%d", &size); err != nil {
			continue
		}
		if size <= 70 {
			selected = append(selected, scowlFile{path: f, size: size, variant: variant})
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("scowl: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO words (word, lang, frequency, variant) VALUES (?, 'en', ?, ?)
		ON CONFLICT(word, lang) DO UPDATE SET
			frequency = MAX(frequency, excluded.frequency),
			variant = CASE
				WHEN words.variant IS NULL THEN excluded.variant
				WHEN excluded.variant IS NULL THEN words.variant
				WHEN words.variant = excluded.variant THEN words.variant
				ELSE NULL
			END`)
	if err != nil {
		return fmt.Errorf("scowl: prepare: %w", err)
	}
	defer stmt.Close()

	var count int
	for _, sf := range selected {
		freq := scowlFrequency[sf.size]
		n, err := loadSCOWLFile(stmt, sf.path, freq, sf.variant)
		if err != nil {
			return fmt.Errorf("scowl: load %s: %w", sf.path, err)
		}
		count += n
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("scowl: commit: %w", err)
	}
	slog.Info("scowl loaded", "words", count, "files", len(selected))
	return nil
}

func loadSCOWLFile(stmt *sql.Stmt, path string, freq int, variant string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Convert empty variant to SQL NULL
	var variantParam any
	if variant != "" {
		variantParam = variant
	}

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
		if _, err := stmt.Exec(word, freq, variantParam); err != nil {
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

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// isJunkWord returns true if the word should be rejected from the dictionary.
func isJunkWord(word string) bool {
	return word == "" || !hasLetter(word) || containsDigit(word) || containsSpace(word) || containsNonLatin(word)
}
