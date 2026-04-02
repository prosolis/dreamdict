package dictionary

import (
	"database/sql"
	"fmt"
	"strings"
)

func (d *Dictionary) IsValidWord(word, lang string) (bool, error) {
	var exists bool
	err := d.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM words WHERE word = ? AND lang = ?)",
		strings.ToLower(word), lang,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("dictionary: is valid word: %w", err)
	}
	return exists, nil
}

type RandomResult struct {
	Word       string   `json:"word"`
	Difficulty *float64 `json:"difficulty,omitempty"`
}

func (d *Dictionary) RandomWord(lang string, opts Options) (RandomResult, error) {
	query := "SELECT word, difficulty FROM words WHERE lang = ?"
	args := []any{lang}

	if opts.POS != "" {
		query += " AND pos = ?"
		args = append(args, opts.POS)
	}
	if opts.MinLength > 0 {
		query += " AND LENGTH(word) >= ?"
		args = append(args, opts.MinLength)
	}
	if opts.MaxLength > 0 {
		query += " AND LENGTH(word) <= ?"
		args = append(args, opts.MaxLength)
	}
	if opts.MinFrequency > 0 {
		query += " AND frequency >= ?"
		args = append(args, opts.MinFrequency)
	}
	if opts.MinDifficulty > 0 {
		query += " AND difficulty >= ?"
		args = append(args, opts.MinDifficulty)
	}
	if opts.MaxDifficulty > 0 {
		query += " AND difficulty <= ?"
		args = append(args, opts.MaxDifficulty)
	}

	query += " ORDER BY RANDOM() LIMIT 1"

	var result RandomResult
	var diff sql.NullFloat64
	err := d.db.QueryRow(query, args...).Scan(&result.Word, &diff)
	if err == sql.ErrNoRows {
		return RandomResult{}, ErrNoMatch
	}
	if err != nil {
		return RandomResult{}, fmt.Errorf("dictionary: random word: %w", err)
	}
	if diff.Valid {
		result.Difficulty = &diff.Float64
	}
	return result, nil
}

func (d *Dictionary) Define(word, lang string) ([]Definition, error) {
	rows, err := d.db.Query(`
		SELECT d.pos, d.gloss, d.source, d.priority
		FROM definitions d
		JOIN words w ON w.id = d.word_id
		WHERE w.word = ? AND w.lang = ?
		ORDER BY d.priority ASC, d.id ASC`,
		strings.ToLower(word), lang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: define: %w", err)
	}
	defer rows.Close()

	var defs []Definition
	for rows.Next() {
		var def Definition
		var pos sql.NullString
		if err := rows.Scan(&pos, &def.Gloss, &def.Source, &def.Priority); err != nil {
			return nil, fmt.Errorf("dictionary: define scan: %w", err)
		}
		def.POS = pos.String
		defs = append(defs, def)
	}
	if defs == nil {
		defs = []Definition{}
	}
	return defs, rows.Err()
}

func (d *Dictionary) Synonyms(word, lang string) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT s.synonym
		FROM synonyms s
		JOIN words w ON w.id = s.word_id
		WHERE w.word = ? AND w.lang = ?
		ORDER BY s.synonym`,
		strings.ToLower(word), lang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: synonyms: %w", err)
	}
	defer rows.Close()

	var syns []string
	for rows.Next() {
		var syn string
		if err := rows.Scan(&syn); err != nil {
			return nil, fmt.Errorf("dictionary: synonyms scan: %w", err)
		}
		syns = append(syns, syn)
	}
	if syns == nil {
		syns = []string{}
	}
	return syns, rows.Err()
}

func (d *Dictionary) Translate(word, fromLang, toLang string) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT t.translation
		FROM translations t
		JOIN words w ON w.id = t.word_id
		WHERE w.word = ? AND w.lang = ? AND t.target_lang = ?
		ORDER BY t.translation`,
		strings.ToLower(word), fromLang, toLang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: translate: %w", err)
	}
	defer rows.Close()

	var trans []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("dictionary: translate scan: %w", err)
		}
		trans = append(trans, t)
	}
	if trans == nil {
		trans = []string{}
	}
	return trans, rows.Err()
}

func (d *Dictionary) WordCount() (map[string]int, error) {
	rows, err := d.db.Query("SELECT lang, COUNT(*) FROM words GROUP BY lang")
	if err != nil {
		return nil, fmt.Errorf("dictionary: word count: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("dictionary: word count scan: %w", err)
		}
		counts[lang] = count
	}
	return counts, rows.Err()
}

// Frequency returns the frequency score for a word in a language.
// Returns 0 if the word is not found or has no frequency data.
// Higher values indicate more common words.
func (d *Dictionary) Frequency(word, lang string) (int, error) {
	var freq int
	err := d.db.QueryRow(
		"SELECT COALESCE(frequency, 0) FROM words WHERE word = ? AND lang = ?",
		strings.ToLower(word), lang,
	).Scan(&freq)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("dictionary: frequency: %w", err)
	}
	return freq, nil
}

// FrequencyBatch returns frequency scores for multiple words in a language.
// Words not found or without frequency data are returned with a score of 0.
func (d *Dictionary) FrequencyBatch(words []string, lang string) (map[string]int, error) {
	if len(words) == 0 {
		return map[string]int{}, nil
	}

	placeholders := make([]string, len(words))
	args := make([]any, 0, len(words)+1)
	args = append(args, lang)
	for i, w := range words {
		placeholders[i] = "?"
		args = append(args, strings.ToLower(w))
	}

	query := fmt.Sprintf(
		"SELECT word, COALESCE(frequency, 0) FROM words WHERE lang = ? AND word IN (%s)",
		strings.Join(placeholders, ","),
	)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("dictionary: frequency batch: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int, len(words))
	for rows.Next() {
		var word string
		var freq int
		if err := rows.Scan(&word, &freq); err != nil {
			return nil, fmt.Errorf("dictionary: frequency batch scan: %w", err)
		}
		result[word] = freq
	}
	return result, rows.Err()
}

func (d *Dictionary) Meta(key string) (string, error) {
	var val string
	err := d.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("dictionary: meta %s: %w", key, err)
	}
	return val, nil
}

func (d *Dictionary) Antonyms(word, lang string) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT DISTINCT a.antonym
		FROM antonyms a
		JOIN words w ON w.id = a.word_id
		WHERE w.word = ? AND w.lang = ?
		ORDER BY a.antonym`,
		strings.ToLower(word), lang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: antonyms: %w", err)
	}
	defer rows.Close()

	var ants []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("dictionary: antonyms scan: %w", err)
		}
		ants = append(ants, a)
	}
	if ants == nil {
		ants = []string{}
	}
	return ants, rows.Err()
}

// EnglishBacking returns English words that share a synset with the given
// non-English word, providing semantic equivalents with WordNet definitions.
func (d *Dictionary) EnglishBacking(word, lang string) ([]EnglishEquivalent, error) {
	// Find synsets for this word, then find English words in those synsets,
	// along with their WordNet definitions.
	rows, err := d.db.Query(`
		SELECT DISTINCT ew.word,
			COALESCE(
				(SELECT d.gloss FROM definitions d
				 WHERE d.word_id = ew.id AND d.source = 'wordnet' AND d.pos = s.pos
				 ORDER BY d.priority ASC LIMIT 1),
				(SELECT d.gloss FROM definitions d
				 WHERE d.word_id = ew.id AND d.source = 'wordnet'
				 ORDER BY d.priority ASC LIMIT 1),
				''
			) AS gloss,
			s.synset_id
		FROM words w
		JOIN word_synsets ws ON ws.word_id = w.id
		JOIN synsets s ON s.id = ws.synset_id
		JOIN word_synsets ews ON ews.synset_id = s.id
		JOIN words ew ON ew.id = ews.word_id AND ew.lang = 'en'
		WHERE w.word = ? AND w.lang = ?
		ORDER BY s.synset_id, ew.word`,
		strings.ToLower(word), lang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: english backing: %w", err)
	}
	defer rows.Close()

	var results []EnglishEquivalent
	seen := make(map[string]bool)
	for rows.Next() {
		var eq EnglishEquivalent
		if err := rows.Scan(&eq.Word, &eq.Definition, &eq.Synset); err != nil {
			return nil, fmt.Errorf("dictionary: english backing scan: %w", err)
		}
		key := eq.Word + "|" + eq.Synset
		if !seen[key] {
			seen[key] = true
			results = append(results, eq)
		}
	}
	if results == nil {
		results = []EnglishEquivalent{}
	}

	// Fallback to translations table if no synset matches
	if len(results) == 0 {
		trans, err := d.Translate(word, lang, "en")
		if err != nil {
			return nil, err
		}
		for _, t := range trans {
			results = append(results, EnglishEquivalent{Word: t})
		}
	}

	return results, rows.Err()
}

func (d *Dictionary) Pronunciation(word, lang string) ([]Pronunciation, error) {
	rows, err := d.db.Query(`
		SELECT p.format, p.value, p.source
		FROM pronunciations p
		JOIN words w ON w.id = p.word_id
		WHERE w.word = ? AND w.lang = ?
		ORDER BY p.source, p.format`,
		strings.ToLower(word), lang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: pronunciation: %w", err)
	}
	defer rows.Close()

	var prons []Pronunciation
	for rows.Next() {
		var p Pronunciation
		if err := rows.Scan(&p.Format, &p.Value, &p.Source); err != nil {
			return nil, fmt.Errorf("dictionary: pronunciation scan: %w", err)
		}
		prons = append(prons, p)
	}
	if prons == nil {
		prons = []Pronunciation{}
	}
	return prons, rows.Err()
}

func (d *Dictionary) Etymology(word, lang string) (string, error) {
	var text string
	err := d.db.QueryRow(`
		SELECT e.text
		FROM etymology e
		JOIN words w ON w.id = e.word_id
		WHERE w.word = ? AND w.lang = ?
		LIMIT 1`,
		strings.ToLower(word), lang,
	).Scan(&text)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("dictionary: etymology: %w", err)
	}
	return text, nil
}

func (d *Dictionary) DefCount() (map[string]int, error) {
	rows, err := d.db.Query(`
		SELECT w.lang, COUNT(*)
		FROM definitions d
		JOIN words w ON w.id = d.word_id
		GROUP BY w.lang`)
	if err != nil {
		return nil, fmt.Errorf("dictionary: def count: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("dictionary: def count scan: %w", err)
		}
		counts[lang] = count
	}
	return counts, rows.Err()
}
