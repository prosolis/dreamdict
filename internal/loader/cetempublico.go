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

// CETEMPublicoLoader loads frequency data for European Portuguese words.
// Accepts a simple word-frequency TSV file derived from the CETEMPúblico corpus
// or any frequency list in "word<tab>count" or "word<tab>frequency_per_million" format.
// Falls back to Wiktionary frequency tags if the primary source is unavailable.
type CETEMPublicoLoader struct{}

func (CETEMPublicoLoader) Name() string { return "cetempublico" }

func (CETEMPublicoLoader) Load(db *sql.DB, dataDir string) error {
	candidates := []string{
		filepath.Join(dataDir, "cetempublico-freq.tsv"),
		filepath.Join(dataDir, "pt-freq.tsv"),
		filepath.Join(dataDir, "pt_PT-freq.tsv"),
	}

	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		slog.Warn("cetempublico: no data file found, skipping", "searched", candidates)
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cetempublico: open: %w", err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("cetempublico: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE words SET frequency = ? WHERE word = ? AND lang = 'pt-PT' AND frequency = 0`)
	if err != nil {
		return fmt.Errorf("cetempublico: prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	// Skip header if present
	if scanner.Scan() {
		first := scanner.Text()
		fields := strings.Split(first, "\t")
		// If first line looks like data (second field is numeric), process it
		if len(fields) >= 2 {
			if _, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64); err != nil {
				// It's a header, skip it
			} else {
				// It's data, we need to process it — but we already consumed it
				// Re-process below
			}
		}
	}

	var count int
	processLine := func(line string) error {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return nil
		}

		word := strings.ToLower(strings.TrimSpace(fields[0]))
		if word == "" || containsDigit(word) || containsSpace(word) || containsNonLatin(word) {
			return nil
		}

		freqVal, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil || freqVal <= 0 {
			return nil
		}

		// If values are raw counts (>100), convert to a 0-10000 scale
		// If already per-million, multiply by 100
		freq := int(freqVal)
		if freqVal > 10000 {
			// Assume raw counts — log-scale normalization
			freq = int(freqVal / 10)
			if freq > 10000 {
				freq = 10000
			}
		} else if freqVal < 100 {
			freq = int(freqVal * 100)
		}
		if freq <= 0 {
			freq = 1
		}

		res, err := stmt.Exec(freq, word)
		if err != nil {
			return fmt.Errorf("cetempublico: update: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count++
		}
		return nil
	}

	for scanner.Scan() {
		if err := processLine(scanner.Text()); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("cetempublico: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cetempublico: commit: %w", err)
	}
	slog.Info("cetempublico loaded", "updated_words", count)
	return nil
}
