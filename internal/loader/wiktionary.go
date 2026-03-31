package loader

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

type WiktionaryLoader struct {
	Lang     string // "en", "fr", "pt-PT"
	FileName string // e.g. "kaikki-en.jsonl"
}

func (w WiktionaryLoader) Name() string { return "wiktionary-" + w.Lang }

func (w WiktionaryLoader) Load(db *sql.DB, dataDir string) error {
	path := filepath.Join(dataDir, w.FileName)
	return loadWiktionary(db, path, w.Lang)
}

type wiktEntry struct {
	Word     string `json:"word"`
	POS      string `json:"pos"`
	LangCode string `json:"lang_code"`
	Senses   []struct {
		Glosses []string `json:"glosses"`
		Tags    []string `json:"tags"`
	} `json:"senses"`
	Synonyms []struct {
		Word string `json:"word"`
	} `json:"synonyms"`
	Translations []struct {
		Code string `json:"code"`
		Word string `json:"word"`
	} `json:"translations"`
}

var wiktPOSMap = map[string]string{
	"noun": "noun",
	"verb": "verb",
	"adj":  "adjective",
	"adv":  "adverb",
	"name": "noun",
}

var wiktLangCodeMap = map[string]string{
	"fr": "fr",
	"pt": "pt-PT",
	"en": "en",
	"zh": "zh",
}

const (
	defSQL   = `INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority) SELECT id, ?, ?, 'wiktionary', 20 FROM words WHERE word = ? AND lang = ?`
	synSQL   = `INSERT OR IGNORE INTO synonyms (word_id, synonym, source) SELECT id, ?, 'wiktionary' FROM words WHERE word = ? AND lang = ?`
	transSQL = `INSERT OR IGNORE INTO translations (word_id, translation, target_lang, source) SELECT id, ?, ?, 'wiktionary' FROM words WHERE word = ? AND lang = ?`
)

// txBatch owns a transaction and its prepared statements.
// Close() is always safe to call — it rolls back (no-op after commit) and closes stmts.
type txBatch struct {
	tx        *sql.Tx
	stmtDef   *sql.Stmt
	stmtSyn   *sql.Stmt
	stmtTrans *sql.Stmt
}

func newTxBatch(db *sql.DB) (*txBatch, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	b := &txBatch{tx: tx}

	b.stmtDef, err = tx.Prepare(defSQL)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("prepare def: %w", err)
	}
	b.stmtSyn, err = tx.Prepare(synSQL)
	if err != nil {
		b.stmtDef.Close()
		tx.Rollback()
		return nil, fmt.Errorf("prepare syn: %w", err)
	}
	b.stmtTrans, err = tx.Prepare(transSQL)
	if err != nil {
		b.stmtDef.Close()
		b.stmtSyn.Close()
		tx.Rollback()
		return nil, fmt.Errorf("prepare trans: %w", err)
	}
	return b, nil
}

func (b *txBatch) Close() {
	if b == nil {
		return
	}
	b.stmtDef.Close()
	b.stmtSyn.Close()
	b.stmtTrans.Close()
	b.tx.Rollback() // no-op after commit
}

func (b *txBatch) Commit() error {
	b.stmtDef.Close()
	b.stmtSyn.Close()
	b.stmtTrans.Close()
	return b.tx.Commit()
}

func loadWiktionary(db *sql.DB, path, lang string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("wiktionary: open: %w", err)
	}
	defer f.Close()

	batch, err := newTxBatch(db)
	if err != nil {
		return fmt.Errorf("wiktionary: %w", err)
	}
	defer batch.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 10*1024*1024), 10*1024*1024)

	var defCount, synCount, transCount, lineCount int
	commitInterval := 10000

	for scanner.Scan() {
		lineCount++

		var entry wiktEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		word := strings.ToLower(entry.Word)
		if containsDigit(word) || containsSpace(word) {
			continue
		}

		pos := wiktPOSMap[entry.POS]

		// Process senses
		for _, sense := range entry.Senses {
			if hasTag(sense.Tags, "form-of", "alt-of") {
				continue
			}
			// pt-PT Brazil filtering
			if lang == "pt-PT" && hasBrazilTag(sense.Tags) {
				continue
			}

			for _, gloss := range sense.Glosses {
				gloss = strings.TrimSpace(gloss)
				if gloss == "" || strings.HasPrefix(gloss, "(") || len(gloss) > 500 {
					continue
				}
				if _, err := batch.stmtDef.Exec(pos, gloss, word, lang); err != nil {
					return fmt.Errorf("wiktionary: insert def: %w", err)
				}
				defCount++
			}
		}

		// Process synonyms
		for _, syn := range entry.Synonyms {
			synWord := strings.ToLower(syn.Word)
			if synWord == "" || containsDigit(synWord) || containsSpace(synWord) {
				continue
			}
			if _, err := batch.stmtSyn.Exec(synWord, word, lang); err != nil {
				return fmt.Errorf("wiktionary: insert syn: %w", err)
			}
			synCount++
		}

		// Process translations
		for _, tr := range entry.Translations {
			targetLang, ok := wiktLangCodeMap[tr.Code]
			if !ok || targetLang == lang {
				continue
			}
			trWord := strings.ToLower(tr.Word)
			if trWord == "" || containsDigit(trWord) || containsSpace(trWord) {
				continue
			}
			if _, err := batch.stmtTrans.Exec(trWord, targetLang, word, lang); err != nil {
				return fmt.Errorf("wiktionary: insert trans: %w", err)
			}
			transCount++
		}

		// Periodic commit — close current batch and start a new one
		if lineCount%commitInterval == 0 {
			if err := batch.Commit(); err != nil {
				return fmt.Errorf("wiktionary: commit: %w", err)
			}
			batch, err = newTxBatch(db)
			if err != nil {
				return fmt.Errorf("wiktionary: %w", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("wiktionary: scan: %w", err)
	}

	if err := batch.Commit(); err != nil {
		return fmt.Errorf("wiktionary: final commit: %w", err)
	}

	slog.Info("wiktionary loaded", "lang", lang, "definitions", defCount, "synonyms", synCount, "translations", transCount)
	return nil
}

func hasTag(tags []string, targets ...string) bool {
	for _, tag := range tags {
		for _, t := range targets {
			if tag == t {
				return true
			}
		}
	}
	return false
}

func hasBrazilTag(tags []string) bool {
	for _, tag := range tags {
		if tag == "Brazil" || tag == "Brazilian Portuguese" {
			return true
		}
	}
	return false
}
