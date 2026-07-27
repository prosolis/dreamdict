package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prosolis/dreamdict/dictionary"

	_ "modernc.org/sqlite"
)

func setupBenchServer(b *testing.B) *http.ServeMux {
	b.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := dictionary.BootstrapSchema(db); err != nil {
		b.Fatal(err)
	}

	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			b.Fatalf("seed: %s: %v", q, err)
		}
	}

	exec("INSERT INTO meta (key, value) VALUES ('schema_version', '1')")
	exec("INSERT INTO meta (key, value) VALUES ('imported_at', '2025-01-01T00:00:00Z')")

	poses := []string{"noun", "verb", "adjective", "adverb"}
	for i := 1; i <= 500; i++ {
		pos := poses[i%len(poses)]
		word := fmt.Sprintf("word%04d", i)
		exec("INSERT INTO words (id, word, lang, pos, frequency) VALUES (?, ?, 'en', ?, ?)",
			i, word, pos, i%100)
		exec("INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (?, ?, ?, 'wordnet', 10)",
			i, pos, fmt.Sprintf("def of %s", word))
		exec("INSERT INTO synonyms (word_id, synonym, source) VALUES (?, ?, 'wordnet')",
			i, fmt.Sprintf("syn_%s", word))
		exec("INSERT INTO translations (word_id, translation, target_lang, source) VALUES (?, ?, 'fr', 'wiktionary')",
			i, fmt.Sprintf("fr_%s", word))
	}

	dict := dictionary.NewFromDB(db)
	b.Cleanup(func() { db.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /valid", handleValid(dict))
	mux.HandleFunc("GET /random", handleRandom(dict))
	mux.HandleFunc("GET /define", handleDefine(dict))
	mux.HandleFunc("GET /synonyms", handleSynonyms(dict))
	mux.HandleFunc("GET /translate", handleTranslate(dict))
	mux.HandleFunc("GET /health", handleHealth(dict, ":memory:"))
	return mux
}

func benchRequest(b *testing.B, mux *http.ServeMux, path string) {
	b.Helper()
	req := httptest.NewRequest("GET", path, nil)
	b.ResetTimer()
	for b.Loop() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != 200 {
			b.Fatalf("status %d for %s", w.Code, path)
		}
	}
}

func BenchmarkHTTPValid(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/valid?word=word0003&lang=en")
}

func BenchmarkHTTPRandom(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/random?lang=en")
}

func BenchmarkHTTPRandomFiltered(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/random?lang=en&pos=noun&min=5&max=10")
}

func BenchmarkHTTPDefine(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/define?word=word0003&lang=en")
}

func BenchmarkHTTPSynonyms(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/synonyms?word=word0003&lang=en")
}

func BenchmarkHTTPTranslate(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/translate?word=word0003&from=en&to=fr")
}

func BenchmarkHTTPHealth(b *testing.B) {
	mux := setupBenchServer(b)
	benchRequest(b, mux, "/health")
}
