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

func (d *Dictionary) RandomWord(lang string, opts Options) (string, error) {
	query := "SELECT word FROM words WHERE lang = ?"
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

	query += " ORDER BY RANDOM() LIMIT 1"

	var word string
	err := d.db.QueryRow(query, args...).Scan(&word)
	if err == sql.ErrNoRows {
		return "", ErrNoMatch
	}
	if err != nil {
		return "", fmt.Errorf("dictionary: random word: %w", err)
	}
	return word, nil
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
