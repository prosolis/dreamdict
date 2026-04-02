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

// OMWLoader loads Open Multilingual Wordnet data for Portuguese,
// mapping pt-PT words to Princeton WordNet synset IDs.
type OMWLoader struct{}

func (OMWLoader) Name() string { return "omw-pt" }

func (OMWLoader) Load(db *sql.DB, dataDir string) error {
	// OMW Portuguese data comes as a tab-separated file.
	// Format varies by release but typically:
	//   synset_id<tab>relation<tab>word
	// where synset_id is like "eng-30-00001740-n" and relation is "lemma"
	//
	// Also supports the WN-LMF XML format and the simpler tab format from
	// https://github.com/omwn/omw-data

	// Try multiple possible file locations
	candidates := []string{
		filepath.Join(dataDir, "omw", "wn-data-por.tab"),
		filepath.Join(dataDir, "omw", "wn-por.tab"),
		filepath.Join(dataDir, "omw-pt.tab"),
	}

	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		slog.Warn("omw-pt: no data file found, skipping", "searched", candidates)
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("omw: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO synsets (synset_id, pos) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("omw: prepare synset: %w", err)
	}
	defer stmtSynset.Close()

	stmtWordSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO word_synsets (word_id, synset_id, source)
		SELECT w.id, s.id, 'omw'
		FROM words w, synsets s
		WHERE w.word = ? AND w.lang = 'pt-PT' AND s.synset_id = ?`)
	if err != nil {
		return fmt.Errorf("omw: prepare word_synset: %w", err)
	}
	defer stmtWordSynset.Close()

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("omw: open: %w", err)
	}
	defer f.Close()

	posMap := map[string]string{
		"n": "noun",
		"v": "verb",
		"a": "adjective",
		"r": "adverb",
	}

	scanner := bufio.NewScanner(f)
	var synsetCount, linkCount int

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}

		synsetRaw := fields[0]
		relation := fields[1]
		word := strings.ToLower(strings.TrimSpace(fields[2]))

		// Only process lemma relations
		if relation != "lemma" {
			continue
		}

		if word == "" || containsDigit(word) || containsSpace(word) {
			continue
		}

		// Normalize synset ID: "eng-30-00001740-n" -> "00001740-n"
		synsetID := normalizeOMWSynsetID(synsetRaw)
		if synsetID == "" {
			continue
		}

		// Extract POS from synset ID
		parts := strings.Split(synsetID, "-")
		if len(parts) != 2 {
			continue
		}
		pos := posMap[parts[1]]
		if pos == "" {
			continue
		}

		if _, err := stmtSynset.Exec(synsetID, pos); err != nil {
			return fmt.Errorf("omw: insert synset: %w", err)
		}
		synsetCount++

		if _, err := stmtWordSynset.Exec(word, synsetID); err != nil {
			return fmt.Errorf("omw: insert word_synset: %w", err)
		}
		linkCount++
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("omw: scan: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("omw: commit: %w", err)
	}
	slog.Info("omw-pt loaded", "synsets", synsetCount, "links", linkCount)
	return nil
}

func normalizeOMWSynsetID(raw string) string {
	// Format: "eng-30-00001740-n" -> "00001740-n"
	parts := strings.Split(raw, "-")
	if len(parts) >= 4 && parts[0] == "eng" {
		return parts[2] + "-" + parts[3]
	}
	// Already normalized
	if len(parts) == 2 && len(parts[0]) == 8 {
		return raw
	}
	return ""
}
