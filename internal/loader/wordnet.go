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

// ssTypeMap maps WordNet ss_type codes to POS and synset pos suffix.
var ssTypeMap = map[string]string{
	"n": "noun",
	"v": "verb",
	"a": "adjective",
	"s": "adjective", // satellite adjective
	"r": "adverb",
}

// ssTypeSuffix maps WordNet ss_type to synset ID suffix character.
var ssTypeSuffix = map[string]string{
	"n": "n",
	"v": "v",
	"a": "a",
	"s": "a", // satellite adjectives share 'a' namespace
	"r": "r",
}

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

	stmtAnt, err := tx.Prepare(`
		INSERT OR IGNORE INTO antonyms (word_id, antonym, source)
		SELECT id, ?, 'wordnet' FROM words WHERE word = ? AND lang = 'en'`)
	if err != nil {
		return fmt.Errorf("wordnet: prepare ant: %w", err)
	}
	defer stmtAnt.Close()

	stmtSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO synsets (synset_id, pos) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("wordnet: prepare synset: %w", err)
	}
	defer stmtSynset.Close()

	stmtWordSynset, err := tx.Prepare(`
		INSERT OR IGNORE INTO word_synsets (word_id, synset_id, source)
		SELECT w.id, s.id, 'wordnet'
		FROM words w, synsets s
		WHERE w.word = ? AND w.lang = 'en' AND s.synset_id = ?`)
	if err != nil {
		return fmt.Errorf("wordnet: prepare word_synset: %w", err)
	}
	defer stmtWordSynset.Close()

	var defCount, synCount, antCount, synsetCount int

	for file, pos := range posFiles {
		path := filepath.Join(dataDir, "wordnet", "dict", file)
		d, s, a, sc, err := loadWordNetFile(stmtDef, stmtSyn, stmtAnt, stmtSynset, stmtWordSynset, path, pos)
		if err != nil {
			return fmt.Errorf("wordnet: %s: %w", file, err)
		}
		defCount += d
		synCount += s
		antCount += a
		synsetCount += sc
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("wordnet: commit: %w", err)
	}
	slog.Info("wordnet loaded", "definitions", defCount, "synonyms", synCount, "antonyms", antCount, "synsets", synsetCount)
	return nil
}

func loadWordNetFile(stmtDef, stmtSyn, stmtAnt, stmtSynset, stmtWordSynset *sql.Stmt, path, pos string) (int, int, int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer f.Close()

	// Single pass: insert defs/syns/synsets as we go, collect lightweight
	// antonym pointers + synset->words map for deferred antonym resolution.
	type deferredAnt struct {
		srcWord         string
		targetSynsetKey string
		tgtWordIdx      int
	}

	synsetWords := make(map[string][]string) // only stores word lists, not full parse data
	var pendingAnts []deferredAnt
	var defCount, synCount, antCount, synsetCount int

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "  ") {
			continue
		}

		parsed := parseWordNetLine(line)
		if parsed.gloss == "" || len(parsed.words) == 0 {
			continue
		}

		// Build synset->words map (needed for antonym resolution)
		if parsed.synsetID != "" {
			synsetWords[parsed.synsetID] = parsed.words

			if _, err := stmtSynset.Exec(parsed.synsetID, pos); err != nil {
				return 0, 0, 0, 0, err
			}
			synsetCount++

			for _, w := range parsed.words {
				if _, err := stmtWordSynset.Exec(w, parsed.synsetID); err != nil {
					return 0, 0, 0, 0, err
				}
			}
		}

		// Insert definitions
		for _, w := range parsed.words {
			if _, err := stmtDef.Exec(pos, parsed.gloss, w); err != nil {
				return 0, 0, 0, 0, err
			}
			defCount++
		}

		// Insert synonym pairs
		var cleanWords []string
		for _, w := range parsed.words {
			if !strings.Contains(w, "_") {
				cleanWords = append(cleanWords, w)
			}
		}
		for i, w1 := range cleanWords {
			for j, w2 := range cleanWords {
				if i != j {
					if _, err := stmtSyn.Exec(w2, w1); err != nil {
						return 0, 0, 0, 0, err
					}
					synCount++
				}
			}
		}

		// Collect antonym pointers for deferred resolution
		for _, ap := range parsed.antonymPtrs {
			if ap.srcWordIdx < 1 || ap.srcWordIdx > len(parsed.words) {
				continue
			}
			srcWord := strings.ToLower(parsed.words[ap.srcWordIdx-1])
			if strings.Contains(srcWord, "_") {
				continue
			}
			pendingAnts = append(pendingAnts, deferredAnt{
				srcWord:         srcWord,
				targetSynsetKey: ap.targetSynsetKey,
				tgtWordIdx:      ap.tgtWordIdx,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, 0, err
	}

	// Resolve deferred antonyms now that all synsets are mapped
	for _, da := range pendingAnts {
		tgtWords, ok := synsetWords[da.targetSynsetKey]
		if !ok || da.tgtWordIdx < 1 || da.tgtWordIdx > len(tgtWords) {
			continue
		}
		tgtWord := strings.ToLower(tgtWords[da.tgtWordIdx-1])
		if strings.Contains(tgtWord, "_") || tgtWord == da.srcWord {
			continue
		}
		if _, err := stmtAnt.Exec(tgtWord, da.srcWord); err != nil {
			return 0, 0, 0, 0, err
		}
		if _, err := stmtAnt.Exec(da.srcWord, tgtWord); err != nil {
			return 0, 0, 0, 0, err
		}
		antCount += 2
	}

	return defCount, synCount, antCount, synsetCount, nil
}

type wordNetParsed struct {
	words    []string
	gloss    string
	synsetID string
	// antonymPtrs: each entry is {targetSynsetKey, sourceWordIdx, targetWordIdx}
	antonymPtrs []antonymPtr
}

type antonymPtr struct {
	targetSynsetKey string // "offset-pos" e.g. "00123456-a"
	srcWordIdx      int    // 1-based word index in source synset
	tgtWordIdx      int    // 1-based word index in target synset
}

func parseWordNetLine(line string) wordNetParsed {
	// Format: synset_offset lex_filenum ss_type w_cnt word lex_id [word lex_id...] p_cnt [ptr...] | gloss
	glossIdx := strings.Index(line, "| ")
	if glossIdx == -1 {
		return wordNetParsed{}
	}

	gloss := strings.TrimSpace(line[glossIdx+2:])
	if semiIdx := strings.Index(gloss, ";"); semiIdx != -1 {
		gloss = strings.TrimSpace(gloss[:semiIdx])
	}
	if gloss == "" {
		return wordNetParsed{}
	}

	dataPart := line[:glossIdx]
	fields := strings.Fields(dataPart)
	if len(fields) < 6 {
		return wordNetParsed{}
	}

	synsetOffset := fields[0]
	ssType := fields[2]
	posSuffix := ssTypeSuffix[ssType]
	synsetID := ""
	if posSuffix != "" {
		synsetID = synsetOffset + "-" + posSuffix
	}

	wc, err := strconv.ParseInt(fields[3], 16, 0)
	if err != nil {
		return wordNetParsed{}
	}
	wordCount := int(wc)
	if wordCount <= 0 || wordCount > 100 {
		return wordNetParsed{}
	}

	var words []string
	for i := 0; i < wordCount && 4+i*2 < len(fields); i++ {
		w := strings.ToLower(fields[4+i*2])
		words = append(words, w)
	}

	// Parse pointers
	ptrStart := 4 + wordCount*2
	var antPtrs []antonymPtr
	if ptrStart < len(fields) {
		pc, err := strconv.Atoi(fields[ptrStart])
		if err == nil {
			for i := 0; i < pc; i++ {
				base := ptrStart + 1 + i*4
				if base+3 >= len(fields) {
					break
				}
				if fields[base] != "!" {
					continue
				}
				tgtOffset := fields[base+1]
				tgtPosChar := fields[base+2]
				srcTgt := fields[base+3]
				if len(srcTgt) != 4 {
					continue
				}
				srcIdx, e1 := strconv.ParseInt(srcTgt[0:2], 16, 0)
				tgtIdx, e2 := strconv.ParseInt(srcTgt[2:4], 16, 0)
				if e1 != nil || e2 != nil {
					continue
				}
				tgtKey := tgtOffset + "-" + tgtPosChar
				antPtrs = append(antPtrs, antonymPtr{
					targetSynsetKey: tgtKey,
					srcWordIdx:      int(srcIdx),
					tgtWordIdx:      int(tgtIdx),
				})
			}
		}
	}

	return wordNetParsed{words: words, gloss: gloss, synsetID: synsetID, antonymPtrs: antPtrs}
}
