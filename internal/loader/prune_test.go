package loader

import (
	"testing"
)

func TestPruneOrphanWords(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	// Insert a real word with a definition
	db.Exec("INSERT INTO words (word, lang, frequency) VALUES ('cat', 'en', 500)")
	db.Exec("INSERT INTO definitions (word_id, pos, gloss, source) SELECT id, 'noun', 'a feline', 'test' FROM words WHERE word='cat'")

	// Insert a word with only frequency (no definitions) — should survive
	db.Exec("INSERT INTO words (word, lang, frequency) VALUES ('the', 'en', 9999)")

	// Insert a ghost word — no definitions, no frequency, no anything
	db.Exec("INSERT INTO words (word, lang, frequency) VALUES ('cheeseburgered', 'en', 0)")

	// Insert a word with synonyms but no definitions — should survive
	db.Exec("INSERT INTO words (word, lang, frequency) VALUES ('glad', 'en', 0)")
	db.Exec("INSERT INTO synonyms (word_id, synonym, source) SELECT id, 'happy', 'test' FROM words WHERE word='glad'")

	pruner := PruneOrphanWords{}
	if err := pruner.Load(db, ""); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM words").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 surviving words (cat, the, glad), got %d", count)
	}

	// Verify the ghost was deleted
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM words WHERE word='cheeseburgered')").Scan(&exists)
	if exists {
		t.Error("expected 'cheeseburgered' to be pruned")
	}

	// Verify real words survived
	for _, word := range []string{"cat", "the", "glad"} {
		db.QueryRow("SELECT EXISTS(SELECT 1 FROM words WHERE word=?)", word).Scan(&exists)
		if !exists {
			t.Errorf("expected '%s' to survive pruning", word)
		}
	}
}
