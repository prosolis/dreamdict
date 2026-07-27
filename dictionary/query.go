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

// WordVariant returns the regional variant tag for a word ("us", "gb", or "").
func (d *Dictionary) WordVariant(word, lang string) (string, error) {
	var variant sql.NullString
	err := d.db.QueryRow(
		"SELECT variant FROM words WHERE word = ? AND lang = ?",
		strings.ToLower(word), lang,
	).Scan(&variant)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("dictionary: word variant: %w", err)
	}
	return variant.String, nil
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
	if opts.Variant != "" {
		query += " AND variant = ?"
		args = append(args, opts.Variant)
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

// wordListQuery builds a "SELECT <cols> FROM words WHERE ..." statement from
// the filters in opts. Words and WordsWithFreqs share it so a filter added
// here applies to both.
func wordListQuery(cols, lang string, opts Options) (string, []any) {
	query := "SELECT DISTINCT " + cols + " FROM words WHERE lang = ?"
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
	if opts.Variant != "" {
		query += " AND variant = ?"
		args = append(args, opts.Variant)
	}

	query += " ORDER BY word LIMIT 20000"
	return query, args
}

func (d *Dictionary) Words(lang string, opts Options) ([]string, error) {
	query, args := wordListQuery("word", lang, opts)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("dictionary: words: %w", err)
	}
	defer rows.Close()

	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, fmt.Errorf("dictionary: words scan: %w", err)
		}
		words = append(words, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dictionary: words: %w", err)
	}
	if len(words) == 0 {
		return nil, ErrNoMatch
	}
	return words, nil
}

// WordEntry holds a word and its frequency score.
type WordEntry struct {
	Word string
	Freq int
}

// WordsWithFreqs returns matching words with their frequency scores.
func (d *Dictionary) WordsWithFreqs(lang string, opts Options) ([]WordEntry, error) {
	query, args := wordListQuery("word, COALESCE(frequency, 0)", lang, opts)

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("dictionary: words with freqs: %w", err)
	}
	defer rows.Close()

	var entries []WordEntry
	for rows.Next() {
		var e WordEntry
		if err := rows.Scan(&e.Word, &e.Freq); err != nil {
			return nil, fmt.Errorf("dictionary: words with freqs scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dictionary: words with freqs: %w", err)
	}
	if len(entries) == 0 {
		return nil, ErrNoMatch
	}
	return entries, nil
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
	// Forward: find translations stored for this word in fromLang → toLang
	// Reverse: find words in toLang that have this word as a translation to fromLang
	// This makes translations bidirectional: if English kaikki has "house" → "casa" (pt-PT),
	// querying "casa" from pt-PT to en will find "house" via the reverse lookup.
	rows, err := d.db.Query(`
		SELECT DISTINCT result FROM (
			SELECT t.translation AS result
			FROM translations t
			JOIN words w ON w.id = t.word_id
			WHERE w.word = ? AND w.lang = ? AND t.target_lang = ?
			UNION
			SELECT w.word AS result
			FROM translations t
			JOIN words w ON w.id = t.word_id
			WHERE t.translation = ? AND t.target_lang = ? AND w.lang = ?
		) ORDER BY result`,
		// Forward:  word=word, lang=fromLang, target_lang=toLang
		strings.ToLower(word), fromLang, toLang,
		// Reverse:  translation=word, target_lang=fromLang (stored target matches our source), lang=toLang
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

// Equivalents returns words in toLang that mean the same thing as word in
// fromLang, best first.
//
// This is Translate's sibling and, for anything other than English as the
// target, its better half. The translations table is built from Wiktionary
// translation sections, which are thin in the en→X direction: measured against
// the 2,000 commonest English words, it answers for 17% of them into pt-PT.
// Going through shared Princeton WordNet synset ids instead — the same
// mechanism EnglishBacking uses in reverse — answers for 61%, and it is where
// the words a learner actually wants live: "ephemeral" has no en→pt-PT
// translation row at all, but shares a synset with efémero, passageiro and
// transitório.
//
// Results are ordered by how many of the source word's senses each candidate
// shares, then by how common the candidate is. Sense agreement has to lead:
// "think" belongs to a dozen synsets, and pensar sits in six of them while
// lembrar sits in one — but lembrar is the commoner Portuguese word, so
// frequency alone puts "remember" at the top of "think". Counting the overlap
// first asks which word means the same thing *most often*, which is the
// question. Frequency then breaks the ties, so casa leads firma for "house".
//
// The translations table is still consulted as a fallback, because a word can
// have a translation and no synset link. Between them they cover 62%.
func (d *Dictionary) Equivalents(word, fromLang, toLang string) ([]string, error) {
	rows, err := d.db.Query(`
		SELECT tw.word,
		       COUNT(DISTINCT sws.synset_id) AS overlap,
		       COALESCE(tw.frequency, 0)     AS freq
		FROM words sw
		JOIN word_synsets sws ON sws.word_id = sw.id
		JOIN word_synsets tws ON tws.synset_id = sws.synset_id
		JOIN words tw ON tw.id = tws.word_id AND tw.lang = ?
		WHERE sw.word = ? AND sw.lang = ? AND tw.id != sw.id
		GROUP BY tw.id
		ORDER BY overlap DESC, freq DESC, tw.word`,
		toLang, strings.ToLower(word), fromLang,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: equivalents: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var w string
		var overlap, freq int
		if err := rows.Scan(&w, &overlap, &freq); err != nil {
			return nil, fmt.Errorf("dictionary: equivalents scan: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}
	return d.Translate(word, fromLang, toLang)
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

func (d *Dictionary) TotalWordCount() (int, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM words").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("dictionary: total word count: %w", err)
	}
	return count, nil
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

// Difficulty returns the difficulty score for a word in a language.
// Returns -1 if the word is not found.
func (d *Dictionary) Difficulty(word, lang string) (float64, error) {
	var diff sql.NullFloat64
	err := d.db.QueryRow(
		"SELECT difficulty FROM words WHERE word = ? AND lang = ?",
		strings.ToLower(word), lang,
	).Scan(&diff)
	if err == sql.ErrNoRows {
		return -1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("dictionary: difficulty: %w", err)
	}
	if !diff.Valid {
		return -1, nil
	}
	return diff.Float64, nil
}

// Rhymes returns words that rhyme with the given word by matching CMU phoneme tails.
// English only. Returns up to `limit` results sorted by frequency descending.
func (d *Dictionary) Rhymes(word string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// Get the CMU tail for the input word.
	var tail sql.NullString
	err := d.db.QueryRow(`
		SELECT p.cmu_tail
		FROM pronunciations p
		JOIN words w ON w.id = p.word_id
		WHERE w.word = ? AND w.lang = 'en' AND p.format = 'cmu' AND p.cmu_tail IS NOT NULL
		LIMIT 1`,
		strings.ToLower(word),
	).Scan(&tail)
	if err == sql.ErrNoRows || !tail.Valid || tail.String == "" {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dictionary: rhymes: %w", err)
	}

	// Find other words with the same tail.
	rows, err := d.db.Query(`
		SELECT DISTINCT w.word
		FROM pronunciations p
		JOIN words w ON w.id = p.word_id
		WHERE p.cmu_tail = ? AND p.format = 'cmu' AND w.lang = 'en' AND w.word != ?
		ORDER BY w.frequency DESC
		LIMIT ?`,
		tail.String, strings.ToLower(word), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("dictionary: rhymes: %w", err)
	}
	defer rows.Close()

	var rhymes []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("dictionary: rhymes scan: %w", err)
		}
		rhymes = append(rhymes, r)
	}
	if rhymes == nil {
		rhymes = []string{}
	}
	return rhymes, rows.Err()
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
