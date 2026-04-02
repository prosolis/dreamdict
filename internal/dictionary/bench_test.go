package dictionary

import (
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func setupBenchDB(b *testing.B) (*Dictionary, *sql.DB) {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := BootstrapSchema(db); err != nil {
		b.Fatal(err)
	}

	// Seed a realistic-ish dataset: 1000 words with definitions, synonyms, translations
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			b.Fatalf("seed: %s: %v", q, err)
		}
	}

	exec("INSERT INTO meta (key, value) VALUES ('schema_version', '1')")
	exec("INSERT INTO meta (key, value) VALUES ('imported_at', '2025-01-01T00:00:00Z')")

	poses := []string{"noun", "verb", "adjective", "adverb"}
	langs := []string{"en", "fr", "pt-PT"}

	for i := 1; i <= 1000; i++ {
		lang := langs[i%len(langs)]
		pos := poses[i%len(poses)]
		word := fmt.Sprintf("word%04d", i)
		exec("INSERT INTO words (id, word, lang, pos, frequency) VALUES (?, ?, ?, ?, ?)",
			i, word, lang, pos, i%100)

		// 2 definitions per word
		exec("INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (?, ?, ?, 'wordnet', 10)",
			i, pos, fmt.Sprintf("definition A of %s", word))
		exec("INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (?, ?, ?, 'wiktionary', 20)",
			i, pos, fmt.Sprintf("definition B of %s", word))

		// 2 synonyms
		exec("INSERT INTO synonyms (word_id, synonym, source) VALUES (?, ?, 'wordnet')",
			i, fmt.Sprintf("syn1_%s", word))
		exec("INSERT INTO synonyms (word_id, synonym, source) VALUES (?, ?, 'wordnet')",
			i, fmt.Sprintf("syn2_%s", word))

		// Translation for en words
		if lang == "en" {
			exec("INSERT INTO translations (word_id, translation, target_lang, source) VALUES (?, ?, 'fr', 'wiktionary')",
				i, fmt.Sprintf("fr_%s", word))
		}
	}

	return NewFromDB(db), db
}

func BenchmarkIsValidWord(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	b.Run("hit", func(b *testing.B) {
		for b.Loop() {
			d.IsValidWord("word0003", "en")
		}
	})
	b.Run("miss", func(b *testing.B) {
		for b.Loop() {
			d.IsValidWord("nonexistent", "en")
		}
	})
}

func BenchmarkRandomWord(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	b.Run("no_filter", func(b *testing.B) {
		for b.Loop() {
			d.RandomWord("en", Options{})
		}
	})
	b.Run("with_filters", func(b *testing.B) {
		for b.Loop() {
			d.RandomWord("en", Options{POS: "noun", MinLength: 4, MaxLength: 10})
		}
	})
}

func BenchmarkDefine(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	b.Run("with_defs", func(b *testing.B) {
		for b.Loop() {
			d.Define("word0003", "en")
		}
	})
	b.Run("no_defs", func(b *testing.B) {
		for b.Loop() {
			d.Define("nonexistent", "en")
		}
	})
}

func BenchmarkSynonyms(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	b.Run("with_syns", func(b *testing.B) {
		for b.Loop() {
			d.Synonyms("word0003", "en")
		}
	})
	b.Run("no_syns", func(b *testing.B) {
		for b.Loop() {
			d.Synonyms("nonexistent", "en")
		}
	})
}

func BenchmarkTranslate(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	b.Run("with_trans", func(b *testing.B) {
		for b.Loop() {
			d.Translate("word0003", "en", "fr")
		}
	})
	b.Run("no_trans", func(b *testing.B) {
		for b.Loop() {
			d.Translate("nonexistent", "en", "fr")
		}
	})
}

func BenchmarkWordCount(b *testing.B) {
	d, db := setupBenchDB(b)
	defer db.Close()

	for b.Loop() {
		d.WordCount()
	}
}
