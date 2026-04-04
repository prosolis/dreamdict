package loader

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// PruneOrphanWords removes words that have no evidence of being real:
// no definitions, no synonyms, no antonyms, no translations,
// no pronunciations, no etymology, no synset membership, and zero frequency.
// These are typically mechanical affix expansion artifacts.
type PruneOrphanWords struct{}

func (PruneOrphanWords) Name() string { return "prune-orphans" }

func (PruneOrphanWords) Load(db *sql.DB, _ string) error {
	res, err := db.Exec(`
		DELETE FROM words
		WHERE frequency = 0
		  AND NOT EXISTS (SELECT 1 FROM definitions   d  WHERE d.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM synonyms      s  WHERE s.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM antonyms      a  WHERE a.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM translations  t  WHERE t.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM pronunciations p WHERE p.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM etymology     e  WHERE e.word_id  = words.id)
		  AND NOT EXISTS (SELECT 1 FROM word_synsets  ws WHERE ws.word_id = words.id)
	`)
	if err != nil {
		return fmt.Errorf("prune orphans: %w", err)
	}

	n, _ := res.RowsAffected()
	slog.Info("pruned orphan words", "deleted", n)
	return nil
}
