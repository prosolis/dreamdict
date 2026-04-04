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
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency, variant) VALUES (6, 'color', 'en', 'noun', 800, 'us')")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency, variant) VALUES (7, 'colour', 'en', 'noun', 800, 'gb')")

	// Definitions
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling pleasure', 'wordnet', 10)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling joy or contentment', 'wiktionary', 20)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (4, 'noun', 'um animal felino', 'dicionario', 10)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (4, 'noun', 'um felino doméstico', 'wiktionary', 20)")

	// Synonyms
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (1, 'glad', 'wordnet')")
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (1, 'joyful', 'wordnet')")
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (4, 'bichano', 'wiktionary')")

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
		{"gato", "pt-PT", true},
		{"gato", "en", false},
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

	// pt-PT definitions
	defs, err = d.Define("gato", "pt-PT")
	if err != nil {
		t.Fatalf("Define(gato): %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("Define(gato) returned %d defs, want 2", len(defs))
	}
	if defs[0].Source != "dicionario" {
		t.Errorf("first pt-PT def source = %q, want dicionario", defs[0].Source)
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

	// pt-PT synonyms
	syns, err = d.Synonyms("gato", "pt-PT")
	if err != nil {
		t.Fatalf("Synonyms(gato): %v", err)
	}
	if len(syns) != 1 || syns[0] != "bichano" {
		t.Errorf("Synonyms(gato) = %v, want [bichano]", syns)
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

func TestWords(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	// Add some 5-letter words with varying frequency
	mustExec(t, db, "INSERT INTO words (word, lang, pos, frequency) VALUES ('crane', 'en', 'noun', 200)")
	mustExec(t, db, "INSERT INTO words (word, lang, pos, frequency) VALUES ('house', 'en', 'noun', 800)")
	mustExec(t, db, "INSERT INTO words (word, lang, pos, frequency) VALUES ('light', 'en', 'noun', 500)")
	mustExec(t, db, "INSERT INTO words (word, lang, pos, frequency) VALUES ('quick', 'en', 'adjective', 100)")

	d := NewFromDB(db)

	// All 5-letter English words
	words, err := d.Words("en", Options{MinLength: 5, MaxLength: 5})
	if err != nil {
		t.Fatalf("Words: %v", err)
	}
	if len(words) != 6 { // color, crane, happy, house, light, quick
		t.Errorf("Words returned %d, want 6", len(words))
	}

	// Filter by frequency
	words, err = d.Words("en", Options{MinLength: 5, MaxLength: 5, MinFrequency: 500})
	if err != nil {
		t.Fatalf("Words with freq filter: %v", err)
	}
	if len(words) != 3 { // color(800), house(800), light(500)
		t.Errorf("Words with freq filter returned %d, want 3", len(words))
	}

	// Filter by POS
	words, err = d.Words("en", Options{MinLength: 5, MaxLength: 5, POS: "noun"})
	if err != nil {
		t.Fatalf("Words with POS filter: %v", err)
	}
	if len(words) != 4 { // color, crane, house, light
		t.Errorf("Words with POS filter returned %d, want 4", len(words))
	}

	// No match
	_, err = d.Words("en", Options{MinLength: 100})
	if err != ErrNoMatch {
		t.Errorf("expected ErrNoMatch, got %v", err)
	}

	// Results are ordered alphabetically
	words, err = d.Words("en", Options{MinLength: 5, MaxLength: 5, POS: "noun"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(words); i++ {
		if words[i] < words[i-1] {
			t.Errorf("words not sorted: %q before %q", words[i-1], words[i])
		}
	}
}

func TestPopulateCMUTails(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	// Insert CMU pronunciation entries without tails
	mustExec(t, db, "INSERT INTO pronunciations (word_id, format, value, source) VALUES (5, 'cmu', 'K AE1 T', 'cmudict')")
	mustExec(t, db, "INSERT INTO pronunciations (word_id, format, value, source) VALUES (1, 'cmu', 'HH AE1 P IY0', 'cmudict')")
	// IPA entry should be ignored
	mustExec(t, db, "INSERT INTO pronunciations (word_id, format, value, source) VALUES (2, 'ipa', '/ɹʌn/', 'wiktionary')")

	n, err := PopulateCMUTails(db)
	if err != nil {
		t.Fatalf("PopulateCMUTails: %v", err)
	}
	if n != 2 {
		t.Errorf("PopulateCMUTails returned %d, want 2", n)
	}

	// Verify tails were set correctly
	var tail string
	db.QueryRow("SELECT cmu_tail FROM pronunciations WHERE value = 'K AE1 T'").Scan(&tail)
	if tail != "AE1 T" {
		t.Errorf("cmu_tail for 'K AE1 T' = %q, want 'AE1 T'", tail)
	}

	db.QueryRow("SELECT cmu_tail FROM pronunciations WHERE value = 'HH AE1 P IY0'").Scan(&tail)
	if tail != "AE1 P IY0" {
		t.Errorf("cmu_tail for 'HH AE1 P IY0' = %q, want 'AE1 P IY0'", tail)
	}

	// Running again should be a no-op
	n, err = PopulateCMUTails(db)
	if err != nil {
		t.Fatalf("PopulateCMUTails second run: %v", err)
	}
	if n != 0 {
		t.Errorf("PopulateCMUTails second run returned %d, want 0", n)
	}
}

func TestCmuTail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"K AE1 T", "AE1 T"},
		{"HH AE1 P IY0", "AE1 P IY0"},
		{"IH0 F EH1 M ER0 AH0 L", "EH1 M ER0 AH0 L"},
		{"AH0", "AH0"},       // no stressed vowel, falls back to any vowel
		{"K T S", ""},          // no vowels at all
	}
	for _, tt := range tests {
		got := cmuTail(tt.input)
		if got != tt.want {
			t.Errorf("cmuTail(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
	if counts["en"] != 5 {
		t.Errorf("en word count = %d, want 5", counts["en"])
	}
	if counts["fr"] != 1 {
		t.Errorf("fr word count = %d, want 1", counts["fr"])
	}
	if counts["pt-PT"] != 1 {
		t.Errorf("pt-PT word count = %d, want 1", counts["pt-PT"])
	}

	total, err := d.TotalWordCount()
	if err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Errorf("total word count = %d, want 7", total)
	}
}

func TestWordVariant(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	tests := []struct {
		word, lang string
		want       string
	}{
		{"color", "en", "us"},
		{"colour", "en", "gb"},
		{"cat", "en", ""},       // common English, no variant
		{"gato", "pt-PT", ""},   // non-English, no variant
		{"nonexistent", "en", ""},
	}
	for _, tt := range tests {
		got, err := d.WordVariant(tt.word, tt.lang)
		if err != nil {
			t.Errorf("WordVariant(%q, %q) error: %v", tt.word, tt.lang, err)
			continue
		}
		if got != tt.want {
			t.Errorf("WordVariant(%q, %q) = %q, want %q", tt.word, tt.lang, got, tt.want)
		}
	}
}

func TestVariantFilter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	seedTestData(t, db)
	d := NewFromDB(db)

	// Filter US-only words
	result, err := d.RandomWord("en", Options{Variant: "us"})
	if err != nil {
		t.Fatalf("RandomWord(variant=us): %v", err)
	}
	if result.Word != "color" {
		t.Errorf("RandomWord(variant=us) = %q, want color", result.Word)
	}

	// Filter GB-only words
	result, err = d.RandomWord("en", Options{Variant: "gb"})
	if err != nil {
		t.Fatalf("RandomWord(variant=gb): %v", err)
	}
	if result.Word != "colour" {
		t.Errorf("RandomWord(variant=gb) = %q, want colour", result.Word)
	}

	// Words endpoint with variant filter
	words, err := d.Words("en", Options{Variant: "us"})
	if err != nil {
		t.Fatalf("Words(variant=us): %v", err)
	}
	if len(words) != 1 || words[0] != "color" {
		t.Errorf("Words(variant=us) = %v, want [color]", words)
	}

	// No variant filter returns all
	words, err = d.Words("en", Options{})
	if err != nil {
		t.Fatalf("Words(no variant): %v", err)
	}
	if len(words) != 5 {
		t.Errorf("Words(no variant) returned %d, want 5", len(words))
	}
}
