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
	"unicode/utf8"
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
		filepath.Join(dataDir, "pt_50k.txt"),      // hermitdave/FrequencyWords
		filepath.Join(dataDir, "pt_full.txt"),      // hermitdave/FrequencyWords
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
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("cetempublico: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtUpdate, err := tx.Prepare(`
		UPDATE words SET frequency = ? WHERE word = ? AND lang = 'pt-PT' AND frequency = 0`)
	if err != nil {
		return fmt.Errorf("cetempublico: prepare update: %w", err)
	}
	defer stmtUpdate.Close()


	// Read entire file to detect encoding before parsing.
	// The file is typically <20 MB so this is fine.
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cetempublico: read file: %w", err)
	}

	isLatin1 := !utf8.Valid(rawBytes)
	if isLatin1 {
		rawBytes = []byte(latin1ToUTF8(rawBytes))
	}

	scanner := bufio.NewScanner(strings.NewReader(string(rawBytes)))

	// Peek first line to detect header vs data.
	// A data line has at least one numeric field (either "word count" or "count word").
	var firstLine string
	if scanner.Scan() {
		firstLine = scanner.Text()
		fields := strings.Fields(firstLine)
		if len(fields) >= 2 {
			_, errFirst := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
			_, errLast := strconv.ParseFloat(strings.TrimSpace(fields[len(fields)-1]), 64)
			if errFirst != nil && errLast != nil {
				firstLine = "" // Neither field is numeric — it's a header, skip it
			}
		}
	}

	var count int
	processLine := func(line string) error {
		// Support TSV and space-separated, with either column order:
		//   "word\tcount", "word count", or "count\tword", "count word"
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			fields = strings.Fields(line)
		}
		if len(fields) < 2 {
			return nil
		}

		// Detect column order: if first field is numeric, it's count-first
		var word string
		var freqStr string
		if _, err := strconv.ParseFloat(strings.TrimSpace(fields[0]), 64); err == nil {
			freqStr = strings.TrimSpace(fields[0])
			word = strings.ToLower(strings.TrimSpace(fields[1]))
		} else {
			word = strings.ToLower(strings.TrimSpace(fields[0]))
			freqStr = strings.TrimSpace(fields[1])
		}

		if word == "" || !hasLetter(word) || containsDigit(word) || containsSpace(word) || containsNonLatin(word) {
			return nil
		}

		freqVal, err := strconv.ParseFloat(freqStr, 64)
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

		res, err := stmtUpdate.Exec(freq, word)
		if err != nil {
			return fmt.Errorf("cetempublico: update: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			count++
		}
		return nil
	}

	// Process first line if it was data (not a header)
	if firstLine != "" {
		if err := processLine(firstLine); err != nil {
			return err
		}
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

// latin1ToUTF8 converts ISO-8859-1 bytes to a UTF-8 string.
// Each byte in ISO-8859-1 maps directly to the same Unicode code point.
func latin1ToUTF8(b []byte) string {
	var buf strings.Builder
	buf.Grow(len(b) + len(b)/4) // accented chars expand
	for _, c := range b {
		buf.WriteRune(rune(c))
	}
	return buf.String()
}
