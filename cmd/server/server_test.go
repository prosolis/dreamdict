package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dreamdict/internal/dictionary"

	_ "modernc.org/sqlite"
)

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("mustExec: %s: %v", query, err)
	}
}

func setupTestServer(t *testing.T) (*dictionary.Dictionary, *http.ServeMux) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(on)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := dictionary.BootstrapSchema(db); err != nil {
		t.Fatal(err)
	}

	// Seed data
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('schema_version', '1')")
	mustExec(t, db, "INSERT INTO meta (key, value) VALUES ('imported_at', '2025-01-01T00:00:00Z')")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (1, 'happy', 'en', 'adjective', 0)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (2, 'cat', 'en', 'noun', 0)")
	mustExec(t, db, "INSERT INTO words (id, word, lang, pos, frequency) VALUES (3, 'chat', 'fr', 'noun', 5000)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling pleasure', 'wordnet', 10)")
	mustExec(t, db, "INSERT INTO definitions (word_id, pos, gloss, source, priority) VALUES (1, 'adjective', 'feeling joy', 'wiktionary', 20)")
	mustExec(t, db, "INSERT INTO synonyms (word_id, synonym, source) VALUES (1, 'glad', 'wordnet')")
	mustExec(t, db, "INSERT INTO translations (word_id, translation, target_lang, source) VALUES (2, 'chat', 'fr', 'wiktionary')")

	dict := dictionary.NewFromDB(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /valid", handleValid(dict))
	mux.HandleFunc("GET /random", handleRandom(dict))
	mux.HandleFunc("GET /define", handleDefine(dict))
	mux.HandleFunc("GET /synonyms", handleSynonyms(dict))
	mux.HandleFunc("GET /translate", handleTranslate(dict))
	mux.HandleFunc("GET /health", handleHealth(dict, ":memory:"))

	t.Cleanup(func() { db.Close() })
	return dict, mux
}

func TestValidEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Valid word
	resp, _ := http.Get(srv.URL + "/valid?word=happy&lang=en")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result map[string]bool
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if !result["valid"] {
		t.Error("expected valid=true")
	}

	// Invalid word
	resp, _ = http.Get(srv.URL + "/valid?word=zzzzz&lang=en")
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["valid"] {
		t.Error("expected valid=false")
	}

	// Missing params
	resp, _ = http.Get(srv.URL + "/valid?word=happy")
	if resp.StatusCode != 400 {
		t.Errorf("missing lang: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRandomEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/random?lang=en")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["word"] == "" {
		t.Error("expected non-empty word")
	}

	// No match
	resp, _ = http.Get(srv.URL + "/random?lang=en&min=100")
	if resp.StatusCode != 404 {
		t.Errorf("no match: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestDefineEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/define?word=happy&lang=en")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		Word        string                  `json:"word"`
		Lang        string                  `json:"lang"`
		Definitions []dictionary.Definition `json:"definitions"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Definitions) != 2 {
		t.Errorf("got %d definitions, want 2", len(result.Definitions))
	}
	if result.Definitions[0].Priority > result.Definitions[1].Priority {
		t.Error("definitions not ordered by priority")
	}

	// Word with no definitions
	resp, _ = http.Get(srv.URL + "/define?word=cat&lang=en")
	if resp.StatusCode != 200 {
		t.Fatalf("no defs: status = %d, want 200", resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Definitions) != 0 {
		t.Errorf("got %d definitions, want 0", len(result.Definitions))
	}
}

func TestSynonymsEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/synonyms?word=happy&lang=en")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		Synonyms []string `json:"synonyms"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Synonyms) != 1 {
		t.Errorf("got %d synonyms, want 1", len(result.Synonyms))
	}

	// No synonyms
	resp, _ = http.Get(srv.URL + "/synonyms?word=cat&lang=en")
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Synonyms) != 0 {
		t.Errorf("got %d synonyms, want 0", len(result.Synonyms))
	}
}

func TestTranslateEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/translate?word=cat&from=en&to=fr")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		Translations []string `json:"translations"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Translations) != 1 || result.Translations[0] != "chat" {
		t.Errorf("got %v, want [chat]", result.Translations)
	}

	// No translations
	resp, _ = http.Get(srv.URL + "/translate?word=happy&from=en&to=fr")
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if len(result.Translations) != 0 {
		t.Errorf("got %d, want 0", len(result.Translations))
	}
}

func TestHealthEndpoint(t *testing.T) {
	_, mux := setupTestServer(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/health")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if result["status"] != "ok" {
		t.Errorf("status = %v, want ok", result["status"])
	}
	counts, ok := result["word_counts"].(map[string]any)
	if !ok {
		t.Fatal("word_counts not a map")
	}
	if counts["en"].(float64) != 2 {
		t.Errorf("en word count = %v, want 2", counts["en"])
	}
}
