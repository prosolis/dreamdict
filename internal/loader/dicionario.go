package loader

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type DicionarioLoader struct{}

func (DicionarioLoader) Name() string { return "dicionario" }

func (DicionarioLoader) Load(db *sql.DB, dataDir string) error {
	path := filepath.Join(dataDir, "dicionario-aberto.xml")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("dicionario: open: %w", err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("dicionario: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtDef, err := tx.Prepare(`
		INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority)
		SELECT id, ?, ?, 'dicionario-aberto', 10 FROM words WHERE word = ? AND lang = 'pt-PT'`)
	if err != nil {
		return fmt.Errorf("dicionario: prepare: %w", err)
	}
	defer stmtDef.Close()

	var defCount, decodeErrors int

	decoder := xml.NewDecoder(f)
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("dicionario: xml decode: %w", err)
		}

		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "entry" {
			continue
		}

		var entry struct {
			Form struct {
				Orth string `xml:"orth"`
			} `xml:"form"`
			Senses []struct {
				GramGrp struct {
					POS string `xml:"pos"`
				} `xml:"gramGrp"`
				Def string `xml:"def"`
			} `xml:"sense"`
		}
		if err := decoder.DecodeElement(&entry, &se); err != nil {
			decodeErrors++
			slog.Debug("dicionario: skipping entry", "error", err)
			continue
		}

		word := strings.ToLower(strings.TrimSpace(entry.Form.Orth))
		if isJunkWord(word) {
			continue
		}

		for _, sense := range entry.Senses {
			gloss := strings.TrimSpace(sense.Def)
			if gloss == "" {
				continue
			}
			pos := mapPtPOS(strings.TrimSpace(sense.GramGrp.POS))
			if _, err := stmtDef.Exec(pos, gloss, word); err != nil {
				return fmt.Errorf("dicionario: insert: %w", err)
			}
			defCount++
		}
	}

	if decodeErrors > 0 {
		slog.Warn("dicionario: skipped malformed entries", "count", decodeErrors)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dicionario: commit: %w", err)
	}
	slog.Info("dicionario loaded", "definitions", defCount)
	return nil
}

func mapPtPOS(pos string) string {
	pos = strings.ToLower(pos)
	switch {
	case strings.Contains(pos, "noun"), strings.HasPrefix(pos, "n."), strings.HasPrefix(pos, "s."),
		strings.HasPrefix(pos, "m."), strings.HasPrefix(pos, "f."):
		return "noun"
	case strings.Contains(pos, "verb"), strings.HasPrefix(pos, "v."):
		return "verb"
	case strings.Contains(pos, "adj"):
		return "adjective"
	case strings.Contains(pos, "adv"):
		return "adverb"
	default:
		return ""
	}
}
