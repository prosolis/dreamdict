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
	Senses []struct {
		Glosses  []string `json:"glosses"`
		Tags     []string `json:"tags"`
		Synonyms []struct {
			Word string `json:"word"`
		} `json:"synonyms"`
		Antonyms []struct {
			Word string `json:"word"`
		} `json:"antonyms"`
	} `json:"senses"`
	Synonyms []struct {
		Word string `json:"word"`
	} `json:"synonyms"`
	Antonyms []struct {
		Word string `json:"word"`
	} `json:"antonyms"`
	Translations []struct {
		Code string `json:"code"`
		Word string `json:"word"`
	} `json:"translations"`
	Sounds []struct {
		IPA string `json:"ipa"`
	} `json:"sounds"`
	EtymologyText string `json:"etymology_text"`
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
	antSQL   = `INSERT OR IGNORE INTO antonyms (word_id, antonym, source) SELECT id, ?, 'wiktionary' FROM words WHERE word = ? AND lang = ?`
	pronSQL  = `INSERT OR IGNORE INTO pronunciations (word_id, format, value, source) SELECT id, 'ipa', ?, 'wiktionary' FROM words WHERE word = ? AND lang = ?`
	etymSQL  = `INSERT OR IGNORE INTO etymology (word_id, text, source) SELECT id, ?, 'wiktionary' FROM words WHERE word = ? AND lang = ?`
)

// txBatch owns a transaction and its prepared statements.
// Close() is always safe to call — it rolls back (no-op after commit) and closes stmts.
type txBatch struct {
	tx        *sql.Tx
	stmtDef   *sql.Stmt
	stmtSyn   *sql.Stmt
	stmtTrans *sql.Stmt
	stmtAnt   *sql.Stmt
	stmtPron  *sql.Stmt
	stmtEtym  *sql.Stmt
}

func newTxBatch(db *sql.DB) (*txBatch, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	b := &txBatch{tx: tx}

	prepareOrRollback := func(sql string) (*sql.Stmt, error) {
		s, e := tx.Prepare(sql)
		if e != nil {
			b.closeStmts()
			tx.Rollback()
		}
		return s, e
	}

	if b.stmtDef, err = prepareOrRollback(defSQL); err != nil {
		return nil, fmt.Errorf("prepare def: %w", err)
	}
	if b.stmtSyn, err = prepareOrRollback(synSQL); err != nil {
		return nil, fmt.Errorf("prepare syn: %w", err)
	}
	if b.stmtTrans, err = prepareOrRollback(transSQL); err != nil {
		return nil, fmt.Errorf("prepare trans: %w", err)
	}
	if b.stmtAnt, err = prepareOrRollback(antSQL); err != nil {
		return nil, fmt.Errorf("prepare ant: %w", err)
	}
	if b.stmtPron, err = prepareOrRollback(pronSQL); err != nil {
		return nil, fmt.Errorf("prepare pron: %w", err)
	}
	if b.stmtEtym, err = prepareOrRollback(etymSQL); err != nil {
		return nil, fmt.Errorf("prepare etym: %w", err)
	}
	return b, nil
}

func (b *txBatch) closeStmts() {
	if b.stmtDef != nil {
		b.stmtDef.Close()
	}
	if b.stmtSyn != nil {
		b.stmtSyn.Close()
	}
	if b.stmtTrans != nil {
		b.stmtTrans.Close()
	}
	if b.stmtAnt != nil {
		b.stmtAnt.Close()
	}
	if b.stmtPron != nil {
		b.stmtPron.Close()
	}
	if b.stmtEtym != nil {
		b.stmtEtym.Close()
	}
}

func (b *txBatch) Close() {
	if b == nil {
		return
	}
	b.closeStmts()
	b.tx.Rollback() // no-op after commit
}

func (b *txBatch) Commit() error {
	b.closeStmts()
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

	var defCount, synCount, transCount, antCount, pronCount, etymCount, lineCount int
	commitInterval := 50000

	for scanner.Scan() {
		lineCount++

		var entry wiktEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		word := strings.ToLower(entry.Word)
		if isJunkWord(word) {
			continue
		}

		pos := wiktPOSMap[entry.POS]

		// Process senses (definitions + sense-level synonyms/antonyms)
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

			// Sense-level synonyms
			for _, syn := range sense.Synonyms {
				synWord := strings.ToLower(syn.Word)
				if synWord == "" || containsDigit(synWord) || containsSpace(synWord) {
					continue
				}
				if _, err := batch.stmtSyn.Exec(synWord, word, lang); err != nil {
					return fmt.Errorf("wiktionary: insert syn: %w", err)
				}
				synCount++
			}

			// Sense-level antonyms
			for _, ant := range sense.Antonyms {
				antWord := strings.ToLower(ant.Word)
				if antWord == "" || containsDigit(antWord) || containsSpace(antWord) {
					continue
				}
				if _, err := batch.stmtAnt.Exec(antWord, word, lang); err != nil {
					return fmt.Errorf("wiktionary: insert ant: %w", err)
				}
				antCount++
			}
		}

		// Process top-level synonyms
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

		// Process top-level antonyms
		for _, ant := range entry.Antonyms {
			antWord := strings.ToLower(ant.Word)
			if antWord == "" || containsDigit(antWord) || containsSpace(antWord) {
				continue
			}
			if _, err := batch.stmtAnt.Exec(antWord, word, lang); err != nil {
				return fmt.Errorf("wiktionary: insert ant: %w", err)
			}
			antCount++
		}

		// Process pronunciations (IPA)
		for _, sound := range entry.Sounds {
			ipa := strings.TrimSpace(sound.IPA)
			if ipa == "" {
				continue
			}
			if _, err := batch.stmtPron.Exec(ipa, word, lang); err != nil {
				return fmt.Errorf("wiktionary: insert pron: %w", err)
			}
			pronCount++
			break // only first IPA per entry
		}

		// Process etymology
		etymText := strings.TrimSpace(entry.EtymologyText)
		if etymText != "" && len(etymText) > 10 && !strings.HasPrefix(etymText, "See ") {
			if _, err := batch.stmtEtym.Exec(etymText, word, lang); err != nil {
				return fmt.Errorf("wiktionary: insert etym: %w", err)
			}
			etymCount++
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

	slog.Info("wiktionary loaded", "lang", lang, "definitions", defCount, "synonyms", synCount, "translations", transCount, "antonyms", antCount, "pronunciations", pronCount, "etymologies", etymCount)
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
