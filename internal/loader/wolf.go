package loader

import (
	"compress/bzip2"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type WOLFLoader struct{}

func (WOLFLoader) Name() string { return "wolf" }

func (WOLFLoader) Load(db *sql.DB, dataDir string) error {
	// Try decompressed first, then compressed
	path := filepath.Join(dataDir, "wolf.xml")
	var reader io.Reader
	var closer io.Closer

	if f, err := os.Open(path); err == nil {
		reader = f
		closer = f
	} else {
		bzPath := filepath.Join(dataDir, "wolf-1.0b4.xml.bz2")
		f, err := os.Open(bzPath)
		if err != nil {
			return fmt.Errorf("wolf: open: %w (also tried %s)", err, path)
		}
		reader = bzip2.NewReader(f)
		closer = f
	}
	defer closer.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("wolf: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtDef, err := tx.Prepare(`
		INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority)
		SELECT id, ?, ?, 'wolf', 10 FROM words WHERE word = ? AND lang = 'fr'`)
	if err != nil {
		return fmt.Errorf("wolf: prepare def: %w", err)
	}
	defer stmtDef.Close()

	stmtSyn, err := tx.Prepare(`
		INSERT OR IGNORE INTO synonyms (word_id, synonym, source)
		SELECT id, ?, 'wolf' FROM words WHERE word = ? AND lang = 'fr'`)
	if err != nil {
		return fmt.Errorf("wolf: prepare syn: %w", err)
	}
	defer stmtSyn.Close()

	stmtSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO synsets (synset_id, pos) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("wolf: prepare synset: %w", err)
	}
	defer stmtSynset.Close()

	stmtWordSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO word_synsets (word_id, synset_id, source)
		SELECT w.id, s.id, 'wolf'
		FROM words w, synsets s
		WHERE w.word = ? AND w.lang = 'fr' AND s.synset_id = ?`)
	if err != nil {
		return fmt.Errorf("wolf: prepare word_synset: %w", err)
	}
	defer stmtWordSynset.Close()

	posMap := map[string]string{
		"n": "noun",
		"v": "verb",
		"a": "adjective",
		"r": "adverb",
		"b": "adverb", // WOLF uses "b" for adverb
	}

	var defCount, synCount, synsetCount, decodeErrors int

	decoder := xml.NewDecoder(reader)
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("wolf: xml decode: %w", err)
		}

		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "SYNSET" {
			continue
		}

		var synset struct {
			ID       string `xml:"ID"`
			IDAlt    string `xml:"id,attr"`
			POS      string `xml:"POS"`
			Literals []struct {
				Value string `xml:",chardata"`
			} `xml:"SYNONYM>LITERAL"`
			DEF string `xml:"DEF"`
		}
		if err := decoder.DecodeElement(&synset, &se); err != nil {
			decodeErrors++
			slog.Debug("wolf: skipping synset", "error", err)
			continue
		}

		pos := posMap[strings.ToLower(strings.TrimSpace(synset.POS))]
		gloss := strings.TrimSpace(synset.DEF)

		// WOLF synset IDs are Princeton WordNet IDs (e.g., "eng-30-00914031-a")
		// Normalize to our format: "00914031-a"
		rawID := synset.ID
		if rawID == "" {
			rawID = synset.IDAlt
		}
		wolfSynsetID := normalizeWOLFSynsetID(rawID)

		var words []string
		for _, lit := range synset.Literals {
			w := strings.ToLower(strings.TrimSpace(lit.Value))
			if !isJunkWord(w) {
				words = append(words, w)
			}
		}

		// Insert synset and link words
		if wolfSynsetID != "" && pos != "" {
			if _, err := stmtSynset.Exec(wolfSynsetID, pos); err != nil {
				return fmt.Errorf("wolf: insert synset: %w", err)
			}
			synsetCount++
			for _, w := range words {
				if _, err := stmtWordSynset.Exec(w, wolfSynsetID); err != nil {
					return fmt.Errorf("wolf: insert word_synset: %w", err)
				}
			}
		}

		if gloss != "" {
			for _, w := range words {
				if _, err := stmtDef.Exec(pos, gloss, w); err != nil {
					return fmt.Errorf("wolf: insert def: %w", err)
				}
				defCount++
			}
		}

		for i, w1 := range words {
			for j, w2 := range words {
				if i == j {
					continue
				}
				if _, err := stmtSyn.Exec(w2, w1); err != nil {
					return fmt.Errorf("wolf: insert syn: %w", err)
				}
				synCount++
			}
		}
	}

	if decodeErrors > 0 {
		slog.Warn("wolf: skipped malformed entries", "count", decodeErrors)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("wolf: commit: %w", err)
	}
	slog.Info("wolf loaded", "definitions", defCount, "synonyms", synCount, "synsets", synsetCount)
	return nil
}

// normalizeWOLFSynsetID extracts the Princeton synset ID from WOLF format.
// WOLF uses "eng-30-XXXXXXXX-P" format; we want "XXXXXXXX-P".
// WOLF uses "b" for adverb POS but WordNet uses "r", so we remap.
func normalizeWOLFSynsetID(wolfID string) string {
	wolfID = strings.TrimSpace(wolfID)
	// Common format: "eng-30-00914031-a"
	parts := strings.Split(wolfID, "-")
	if len(parts) >= 4 && parts[0] == "eng" {
		posSuffix := wolfPosSuffix(parts[3])
		return parts[2] + "-" + posSuffix
	}
	// Fallback: if it's already in our format
	if len(parts) == 2 && len(parts[0]) == 8 {
		return parts[0] + "-" + wolfPosSuffix(parts[1])
	}
	return ""
}

// wolfPosSuffix maps WOLF POS codes to WordNet synset ID suffixes.
func wolfPosSuffix(pos string) string {
	pos = strings.TrimSpace(pos)
	switch pos {
	case "b":
		return "r" // WOLF "b" (adverb) → WordNet "r"
	default:
		return pos
	}
}
