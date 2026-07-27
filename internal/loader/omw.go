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

// OMWLoader loads Open Multilingual Wordnet data for one language,
// mapping its words to Princeton WordNet synset IDs. These mappings are what
// /backing uses to find English semantic equivalents.
type OMWLoader struct {
	Lang     string   // language of the words to link, e.g. "pt-PT"
	Suffix   string   // OMW ISO 639-3 code used in file names, e.g. "por"
	Extra    []string // additional candidate paths relative to dataDir
	LangName string   // optional loader-name suffix; defaults to Lang
}

func (o OMWLoader) Name() string {
	if o.LangName != "" {
		return "omw-" + o.LangName
	}
	return "omw-" + o.Lang
}

func (o OMWLoader) Load(db *sql.DB, dataDir string) error {
	// OMW data comes as a tab-separated file.
	// Format varies by release but typically:
	//   synset_id<tab>relation<tab>word
	// where synset_id is like "eng-30-00001740-n" and relation is "lemma"
	//
	// Also supports the WN-LMF XML format and the simpler tab format from
	// https://github.com/omwn/omw-data

	// Try multiple possible file locations
	candidates := []string{
		filepath.Join(dataDir, "omw", "wn-data-"+o.Suffix+".tab"),
		filepath.Join(dataDir, "omw", "wn-"+o.Suffix+".tab"),
	}
	for _, e := range o.Extra {
		candidates = append(candidates, filepath.Join(dataDir, e))
	}

	var path string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			path = c
			break
		}
	}
	if path == "" {
		slog.Warn(o.Name()+": no data file found, skipping", "searched", candidates)
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
		WHERE w.word = ? AND w.lang = ? AND s.synset_id = ?`)
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

		// Only process lemma relations. Wordnets sourced from the MCR
		// (Spanish among them) prefix the relation with their language code,
		// e.g. "spa:lemma"; OpenWN-PT writes a bare "lemma".
		if !isOMWLemmaRelation(relation) {
			continue
		}

		if isJunkWord(word) {
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

		if _, err := stmtWordSynset.Exec(word, o.Lang, synsetID); err != nil {
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
	slog.Info("omw loaded", "loader", o.Name(), "lang", o.Lang, "synsets", synsetCount, "links", linkCount)
	return nil
}

// isOMWLemmaRelation reports whether a relation column marks a lemma entry.
// Accepts both "lemma" and the MCR's "<iso639-3>:lemma" form.
func isOMWLemmaRelation(relation string) bool {
	return relation == "lemma" || strings.HasSuffix(relation, ":lemma")
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
