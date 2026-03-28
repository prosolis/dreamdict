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

type WordNetLoader struct{}

func (WordNetLoader) Name() string { return "wordnet" }

func (WordNetLoader) Load(db *sql.DB, dataDir string) error {
	posFiles := map[string]string{
		"data.noun": "noun",
		"data.verb": "verb",
		"data.adj":  "adjective",
		"data.adv":  "adverb",
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("wordnet: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtDef, err := tx.Prepare(`
		INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority)
		SELECT id, ?, ?, 'wordnet', 10 FROM words WHERE word = ? AND lang = 'en'`)
	if err != nil {
		return fmt.Errorf("wordnet: prepare def: %w", err)
	}
	defer stmtDef.Close()

	stmtSyn, err := tx.Prepare(`
		INSERT OR IGNORE INTO synonyms (word_id, synonym, source)
		SELECT id, ?, 'wordnet' FROM words WHERE word = ? AND lang = 'en'`)
	if err != nil {
		return fmt.Errorf("wordnet: prepare syn: %w", err)
	}
	defer stmtSyn.Close()

	var defCount, synCount int

	for file, pos := range posFiles {
		path := filepath.Join(dataDir, "wordnet", "dict", file)
		d, s, err := loadWordNetFile(stmtDef, stmtSyn, path, pos)
		if err != nil {
			return fmt.Errorf("wordnet: %s: %w", file, err)
		}
		defCount += d
		synCount += s
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("wordnet: commit: %w", err)
	}
	slog.Info("wordnet loaded", "definitions", defCount, "synonyms", synCount)
	return nil
}

func loadWordNetFile(stmtDef, stmtSyn *sql.Stmt, path, pos string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var defCount, synCount int
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// Skip header lines (start with two spaces)
		if strings.HasPrefix(line, "  ") {
			continue
		}

		words, gloss := parseWordNetLine(line)
		if gloss == "" || len(words) == 0 {
			continue
		}

		// Insert definitions for each word
		for _, w := range words {
			if _, err := stmtDef.Exec(pos, gloss, w); err != nil {
				return 0, 0, err
			}
			defCount++
		}

		// Insert synonym pairs (skip words with underscores)
		var cleanWords []string
		for _, w := range words {
			if !strings.Contains(w, "_") {
				cleanWords = append(cleanWords, w)
			}
		}
		for i, w1 := range cleanWords {
			for j, w2 := range cleanWords {
				if i == j {
					continue
				}
				if _, err := stmtSyn.Exec(w2, w1); err != nil {
					return 0, 0, err
				}
				synCount++
			}
		}
	}
	return defCount, synCount, scanner.Err()
}

func parseWordNetLine(line string) ([]string, string) {
	// Format: synset_offset lex_filenum ss_type w_cnt word lex_id [word lex_id...] p_cnt [ptr...] | gloss
	glossIdx := strings.Index(line, "| ")
	if glossIdx == -1 {
		return nil, ""
	}

	gloss := strings.TrimSpace(line[glossIdx+2:])
	// Take gloss before first ";" (drops usage examples)
	if semiIdx := strings.Index(gloss, ";"); semiIdx != -1 {
		gloss = strings.TrimSpace(gloss[:semiIdx])
	}
	if gloss == "" {
		return nil, ""
	}

	dataPart := line[:glossIdx]
	fields := strings.Fields(dataPart)
	if len(fields) < 6 {
		return nil, ""
	}

	// fields[3] = w_cnt (hex)
	wc, err := strconv.ParseInt(fields[3], 16, 0)
	if err != nil {
		slog.Debug("wordnet: bad word count", "field", fields[3], "error", err)
		return nil, ""
	}
	wordCount := int(wc)
	if wordCount <= 0 || wordCount > 100 {
		slog.Debug("wordnet: unreasonable word count", "count", wordCount)
		return nil, ""
	}

	var words []string
	for i := 0; i < wordCount && 4+i*2 < len(fields); i++ {
		w := strings.ToLower(fields[4+i*2])
		words = append(words, w)
	}
	return words, gloss
}
