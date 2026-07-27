package loader

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/prosolis/dreamdict/dictionary"

	_ "modernc.org/sqlite"
)

func setupWiktTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := dictionary.BootstrapSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func writeJSONL(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFormOfFiltering(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	// Seed a word so definitions can reference it
	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('run', 'en')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonl := `{"word":"run","pos":"verb","lang_code":"en","senses":[{"glosses":["to move quickly"],"tags":[]},{"glosses":["past tense of run"],"tags":["form-of"]},{"glosses":["alternative form"],"tags":["alt-of"]}],"synonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "en"); err != nil {
		t.Fatal(err)
	}

	// Only the first sense should be loaded — form-of and alt-of should be filtered
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM definitions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 definition (form-of filtered), got %d", count)
	}

	// Verify the correct gloss was kept
	var gloss string
	if err := db.QueryRow("SELECT gloss FROM definitions").Scan(&gloss); err != nil {
		t.Fatal(err)
	}
	if gloss != "to move quickly" {
		t.Errorf("expected 'to move quickly', got %q", gloss)
	}
}

func TestBrazilFiltering(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('autocarro', 'pt-PT')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonl := `{"word":"autocarro","pos":"noun","lang_code":"pt","senses":[{"glosses":["a bus (Portugal)"],"tags":["Portugal"]},{"glosses":["a bus (Brazil)"],"tags":["Brazil"]},{"glosses":["a vehicle for transport"],"tags":[]}],"synonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "pt-PT"); err != nil {
		t.Fatal(err)
	}

	// Brazil-tagged sense should be filtered; Portugal-tagged and untagged should remain
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM definitions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 definitions (Brazil filtered), got %d", count)
	}
}

func TestBrazilianPortugueseTagFiltering(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('ônibus', 'pt-PT')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonl := `{"word":"ônibus","pos":"noun","lang_code":"pt","senses":[{"glosses":["a bus"],"tags":["Brazilian Portuguese"]}],"synonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "pt-PT"); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM definitions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 definitions (Brazilian Portuguese filtered), got %d", count)
	}
}

func TestGlossFiltering(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('test', 'en')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jsonl := `{"word":"test","pos":"noun","lang_code":"en","senses":[{"glosses":["a valid definition"],"tags":[]},{"glosses":["(archaic form)"],"tags":[]},{"glosses":[""],"tags":[]}],"synonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "en"); err != nil {
		t.Fatal(err)
	}

	// Only "a valid definition" should pass — parenthetical and empty filtered
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM definitions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 definition (gloss filtering), got %d", count)
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		tags    []string
		targets []string
		want    bool
	}{
		{[]string{"form-of", "verb"}, []string{"form-of"}, true},
		{[]string{"verb"}, []string{"form-of", "alt-of"}, false},
		{[]string{"alt-of"}, []string{"form-of", "alt-of"}, true},
		{nil, []string{"form-of"}, false},
		{[]string{}, []string{"form-of"}, false},
	}
	for _, tt := range tests {
		got := hasTag(tt.tags, tt.targets...)
		if got != tt.want {
			t.Errorf("hasTag(%v, %v) = %v, want %v", tt.tags, tt.targets, got, tt.want)
		}
	}
}

func TestSenseLevelSynonyms(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('happy', 'en')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Synonyms nested inside senses, not top-level
	jsonl := `{"word":"happy","pos":"adj","lang_code":"en","senses":[{"glosses":["feeling joy"],"tags":[],"synonyms":[{"word":"joyful"},{"word":"glad"}],"antonyms":[{"word":"sad"}]}],"synonyms":[],"antonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "en"); err != nil {
		t.Fatal(err)
	}

	var synCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM synonyms").Scan(&synCount); err != nil {
		t.Fatal(err)
	}
	if synCount != 2 {
		t.Errorf("expected 2 sense-level synonyms, got %d", synCount)
	}

	var antCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM antonyms").Scan(&antCount); err != nil {
		t.Fatal(err)
	}
	if antCount != 1 {
		t.Errorf("expected 1 sense-level antonym, got %d", antCount)
	}
}

func TestTopAndSenseLevelSynonymsMerge(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES ('big', 'en')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Both top-level and sense-level synonyms
	jsonl := `{"word":"big","pos":"adj","lang_code":"en","senses":[{"glosses":["of great size"],"tags":[],"synonyms":[{"word":"huge"}]}],"synonyms":[{"word":"large"}],"antonyms":[],"translations":[]}` + "\n"
	path := writeJSONL(t, dir, "test.jsonl", jsonl)

	if err := loadWiktionary(db, path, "en"); err != nil {
		t.Fatal(err)
	}

	var synCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM synonyms").Scan(&synCount); err != nil {
		t.Fatal(err)
	}
	if synCount != 2 {
		t.Errorf("expected 2 synonyms (1 top-level + 1 sense-level), got %d", synCount)
	}
}

func TestHasBrazilTag(t *testing.T) {
	tests := []struct {
		tags []string
		want bool
	}{
		{[]string{"Brazil"}, true},
		{[]string{"Brazilian Portuguese"}, true},
		{[]string{"Portugal"}, false},
		{[]string{"European Portuguese"}, false},
		{nil, false},
		{[]string{}, false},
	}
	for _, tt := range tests {
		got := hasBrazilTag(tt.tags)
		if got != tt.want {
			t.Errorf("hasBrazilTag(%v) = %v, want %v", tt.tags, got, tt.want)
		}
	}
}
