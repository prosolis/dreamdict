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

	posMap := map[string]string{
		"n": "noun",
		"v": "verb",
		"a": "adjective",
		"r": "adverb",
	}

	var defCount, synCount, decodeErrors int

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

		pos := posMap[strings.ToLower(synset.POS)]
		gloss := strings.TrimSpace(synset.DEF)

		var words []string
		for _, lit := range synset.Literals {
			w := strings.ToLower(strings.TrimSpace(lit.Value))
			if w != "" && !containsDigit(w) && !containsSpace(w) {
				words = append(words, w)
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
	slog.Info("wolf loaded", "definitions", defCount, "synonyms", synCount)
	return nil
}
