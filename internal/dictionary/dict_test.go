package dictionary

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := BootstrapSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("mustExec: %s: %v", query, err)
	}
}

func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()

	// Meta
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('schema_version', '1')")
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('imported_at', '2025-01-01T00:00:00Z')")

	// Words
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (1, 'happy', 'en', 'adjective', 0)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (2, 'run', 'en', 'verb', 0)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (3, 'chat', 'fr', 'noun', 5000)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (4, 'gato', 'pt-PT', 'noun', 0)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (5, 'cat', 'en', 'noun', 0)")

	// Definitions
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling pleasure', 'wordnet', 10)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling joy or contentment', 'wiktionary', 20)")

	// Synonyms
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (1, 'glad', 'wordnet')")
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (1, 'joyful', 'wordnet')")

	// Translations
	mustExec(t, db, "INSERT INTO translations (word_id, translation, target_lang, source) VALUES (5, 'chat', 'fr', 'wiktionary')")
	mustExec(t, db, "INSERT INTO translations (word_id, translation, target_lang, source) VALUES (5, 'gato', 'pt-PT', 'wiktionary')")
}

func TestIsValidWord(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	tests := []struct {
		word, lang string
		want       bool
	}{
		{"happy", "en", true},
		{"Happy", "en", true}, // case-insensitive
		{"nonexistent", "en", false},
		{"chat", "fr", true},
		{"chat", "en", false},
	}
	for _, tt := range tests {
		got, err := d.IsValidWord(tt.word, tt.lang)
		if err != nil {
			t.Errorf("IsValidWord(%q, %q) error: %v", tt.word, tt.lang, err)
			continue
		}
		if got != tt.want {
			t.Errorf("IsValidWord(%q, %q) = %v, want %v", tt.word, tt.lang, got, tt.want)
		}
	}
}

func TestRandomWord(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	// Basic random word
	result, err := d.RandomWord("en", Options{})
	if err != nil {
		t.Fatalf("RandomWord: %v", err)
	}
	if result.Word == "" {
		t.Error("RandomWord returned empty string")
	}

	// With POS filter
	result, err = d.RandomWord("en", Options{POS: "adjective"})
	if err != nil {
		t.Fatalf("RandomWord with POS: %v", err)
	}
	if result.Word != "happy" {
		t.Errorf("RandomWord(adjective) = %q, want happy", result.Word)
	}

	// With length filters
	result, err = d.RandomWord("en", Options{MinLength: 4, MaxLength: 5})
	if err != nil {
		t.Fatalf("RandomWord with length: %v", err)
	}
	if len(result.Word) < 4 || len(result.Word) > 5 {
		t.Errorf("RandomWord length %d not in [4,5]", len(result.Word))
	}

	// No match
	_, err = d.RandomWord("en", Options{MinLength: 100})
	if err != ErrNoMatch {
		t.Errorf("expected ErrNoMatch, got %v", err)
	}
}

func TestDefine(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	defs, err := d.Define("happy", "en")
	if err != nil {
		t.Fatalf("Define: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("Define returned %d defs, want 2", len(defs))
	}
	// Priority ordering: wordnet (10) before wiktionary (20)
	if defs[0].Source != "wordnet" {
		t.Errorf("first def source = %q, want wordnet", defs[0].Source)
	}
	if defs[1].Source != "wiktionary" {
		t.Errorf("second def source = %q, want wiktionary", defs[1].Source)
	}

	// No definitions
	defs, err = d.Define("run", "en")
	if err != nil {
		t.Fatalf("Define(run): %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("Define(run) returned %d defs, want 0", len(defs))
	}
}

func TestSynonyms(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	syns, err := d.Synonyms("happy", "en")
	if err != nil {
		t.Fatalf("Synonyms: %v", err)
	}
	if len(syns) != 2 {
		t.Fatalf("Synonyms returned %d, want 2", len(syns))
	}

	// No synonyms
	syns, err = d.Synonyms("run", "en")
	if err != nil {
		t.Fatalf("Synonyms(run): %v", err)
	}
	if len(syns) != 0 {
		t.Errorf("Synonyms(run) returned %d, want 0", len(syns))
	}
}

func TestTranslate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	trans, err := d.Translate("cat", "en", "fr")
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(trans) != 1 || trans[0] != "chat" {
		t.Errorf("Translate(cat, en, fr) = %v, want [chat]", trans)
	}

	// No translations
	trans, err = d.Translate("happy", "en", "fr")
	if err != nil {
		t.Fatalf("Translate(happy): %v", err)
	}
	if len(trans) != 0 {
		t.Errorf("Translate(happy) returned %d, want 0", len(trans))
	}
}

func TestErrNotSeeded(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	// Don't seed — meta table is empty
	d := NewFromDB(db)
	err := d.checkSeeded()
	if err != ErrNotSeeded {
		t.Errorf("expected ErrNotSeeded, got %v", err)
	}
}

func TestGlossDeduplication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	// Insert duplicate gloss — should be ignored by UNIQUE constraint
	_, err := db.Exec("INSERT OR IGNORE INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling pleasure', 'wiktionary', 20)")
	if err != nil {
		t.Fatal(err)
	}
	d := NewFromDB(db)
	defs, err := d.Define("happy", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 2 {
		t.Errorf("expected 2 defs after dedup, got %d", len(defs))
	}
}

func TestSynonymDeduplication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	_, err := db.Exec("INSERT OR IGNORE INTO synonyms (word_id, synonym, source) VALUES (1, 'glad', 'wiktionary')")
	if err != nil {
		t.Fatal(err)
	}
	d := NewFromDB(db)
	syns, err := d.Synonyms("happy", "en")
	if err != nil {
		t.Fatal(err)
	}
	if len(syns) != 2 {
		t.Errorf("expected 2 synonyms after dedup, got %d", len(syns))
	}
}

func TestTranslationDeduplication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	_, err := db.Exec("INSERT OR IGNORE INTO translations (word_id, translation, target_lang, source) VALUES (5, 'chat', 'fr', 'other')")
	if err != nil {
		t.Fatal(err)
	}
	d := NewFromDB(db)
	trans, err := d.Translate("cat", "en", "fr")
	if err != nil {
		t.Fatal(err)
	}
	if len(trans) != 1 {
		t.Errorf("expected 1 translation after dedup, got %d", len(trans))
	}
}

func TestMeta(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	val, err := d.Meta("schema_version")
	if err != nil {
		t.Fatal(err)
	}
	if val != "1" {
		t.Errorf("Meta(schema_version) = %q, want 1", val)
	}

	// Missing key returns empty string, no error
	val, err = d.Meta("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("Meta(nonexistent) = %q, want empty", val)
	}
}

func TestWordCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	counts, err := d.WordCount()
	if err != nil {
		t.Fatal(err)
	}
	if counts["en"] != 3 {
		t.Errorf("en word count = %d, want 3", counts["en"])
	}
	if counts["fr"] != 1 {
		t.Errorf("fr word count = %d, want 1", counts["fr"])
	}
}
