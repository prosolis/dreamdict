package loader

import (
	"database/sql"
	"fmt"
	"log/slog"

	"dreamdict/internal/dictionary"
)

// DifficultyScorer computes a composite difficulty score for all words
// in the database. It runs after all other loaders have completed.
// Score range: 0.0 (easiest) to 1.0 (hardest).
//
// Formula:
//   difficulty = normalize(1/frequency) * 0.5
//              + normalize(length) * 0.3
//              + normalize(syllable_count_approx) * 0.2
//
// Computed entirely in SQL for performance — no row-by-row round trips.
type DifficultyScorer struct{}

func (DifficultyScorer) Name() string { return "difficulty" }

func (DifficultyScorer) Load(db *sql.DB, _ string) error {
	var totalUpdated int64
	for _, lang := range dictionary.Langs() {
		n, err := scoreLang(db, lang)
		if err != nil {
			return fmt.Errorf("difficulty: %s: %w", lang, err)
		}
		totalUpdated += n
	}

	slog.Info("difficulty scored", "total_words", totalUpdated)
	return nil
}

func scoreLang(db *sql.DB, lang string) (int64, error) {
	// Single UPDATE using SQLite math to compute difficulty in-place.
	// log() isn't available in base SQLite, so we approximate the frequency
	// component using a reciprocal: 1.0 / (1.0 + frequency/max_freq).
	// This gives a 0–1 range where 0 = most common, ~1 = rarest.
	//
	// Length and a rough syllable proxy (length/3) are normalized against
	// per-language maximums via a subquery.
	const q = `
		UPDATE words SET difficulty = ROUND(
			CASE
				WHEN stats.max_freq > 0 AND frequency > 0
				THEN (1.0 - CAST(frequency AS REAL) / stats.max_freq) * 0.5
				ELSE 0.5
			END
			+ (CAST(LENGTH(word) AS REAL) / stats.max_len) * 0.3
			+ MIN(1.0, CAST(LENGTH(word) AS REAL) / 3.0 / (stats.max_len / 3.0)) * 0.2
		, 3)
		FROM (
			SELECT
				MAX(frequency) AS max_freq,
				MAX(LENGTH(word)) AS max_len
			FROM words WHERE lang = ?1
		) AS stats
		WHERE words.lang = ?1
	`

	res, err := db.Exec(q, lang)
	if err != nil {
		return 0, fmt.Errorf("update difficulty: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
