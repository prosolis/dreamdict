package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"dreamdict/internal/dictionary"
)

func main() {
	port := flag.Int("port", 0, "Listen port (default 7777)")
	host := flag.String("host", "", "Listen host (default 127.0.0.1)")
	dbPath := flag.String("db", "", "Path to dict.db")
	token := flag.String("token", "", "Bearer token for API auth (or DREAMDICT_TOKEN env)")
	logLevel := flag.String("log-level", "info", "Log level: debug, info, warn, error")
	flag.Parse()

	// Resolve config: flag > env > default
	if *port == 0 {
		if v := os.Getenv("DREAMDICT_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				*port = p
			}
		}
	}
	if *port == 0 {
		*port = 7777
	}
	if *host == "" {
		if v := os.Getenv("DREAMDICT_HOST"); v != "" {
			*host = v
		} else {
			*host = "127.0.0.1"
		}
	}
	if *dbPath == "" {
		if v := os.Getenv("DREAMDICT_DB"); v != "" {
			*dbPath = v
		} else {
			*dbPath = "./dict.db"
		}
	}

	if *token == "" {
		*token = os.Getenv("DREAMDICT_TOKEN")
	}

	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	// Run one-time migrations (e.g. populate cmu_tail for rhyme support)
	// before opening the database read-only.
	if n, err := dictionary.Migrate(*dbPath); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	} else if n > 0 {
		slog.Info("migration complete", "cmu_tails_populated", n)
	}

	dict, err := dictionary.NewReadOnly(*dbPath)
	if err != nil {
		slog.Error("failed to open dictionary", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := dict.Close(); err != nil {
			slog.Error("failed to close dictionary", "error", err)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /valid", handleValid(dict))
	mux.HandleFunc("GET /random", handleRandom(dict))
	mux.HandleFunc("GET /define", handleDefine(dict))
	mux.HandleFunc("GET /synonyms", handleSynonyms(dict))
	mux.HandleFunc("GET /antonyms", handleAntonyms(dict))
	mux.HandleFunc("GET /translate", handleTranslate(dict))
	mux.HandleFunc("GET /backing", handleBacking(dict))
	mux.HandleFunc("GET /frequency", handleFrequency(dict))
	mux.HandleFunc("GET /frequency/batch", handleFrequencyBatch(dict))
	mux.HandleFunc("GET /pronunciation", handlePronunciation(dict))
	mux.HandleFunc("GET /etymology", handleEtymology(dict))
	mux.HandleFunc("GET /difficulty", handleDifficulty(dict))
	mux.HandleFunc("GET /rhyme", handleRhyme(dict))
	mux.HandleFunc("GET /health", handleHealth(dict, *dbPath))

	serverStart = time.Now()
	addr := fmt.Sprintf("%s:%d", *host, *port)

	var handler http.Handler = mux
	if *token != "" {
		handler = authMiddleware(*token, mux)
		slog.Info("bearer token auth enabled")
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      loggingMiddleware(handler),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("dreamdict listening", "addr", addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

// statusRecorder wraps ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

var (
	queryCount  atomic.Uint64
	errorCount  atomic.Uint64
	serverStart time.Time
	lastPath    atomic.Value // stores string
)

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		n := queryCount.Add(1)
		if rec.status >= 400 {
			errorCount.Add(1)
		}
		lastPath.Store(r.URL.Path)
		uptime := time.Since(serverStart).Truncate(time.Second)
		rps := float64(n) / time.Since(serverStart).Seconds()
		errs := errorCount.Load()
		last, _ := lastPath.Load().(string)
		fmt.Fprintf(os.Stdout, "\r\033[2KQueries: %d | Errors: %d | Req/s: %.1f | Uptime: %s | Last: %s",
			n, errs, rps, uptime, last)
		slog.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"status", rec.status,
			"duration", time.Since(start).Round(time.Microsecond),
			"remote", r.RemoteAddr,
		)
	})
}

func authMiddleware(token string, next http.Handler) http.Handler {
	expected := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health endpoint is exempt so monitoring works without credentials
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(expected)) != 1 {
			writeError(w, 401, "unauthorized", "invalid or missing bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, errCode, message string) {
	writeJSON(w, status, map[string]string{"error": errCode, "message": message})
}

func handleValid(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		valid, err := dict.IsValidWord(word, lang)
		if err != nil {
			slog.Error("valid", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]bool{"valid": valid})
	}
}

func handleRandom(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			writeError(w, 400, "bad_request", "lang is required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		opts := dictionary.Options{
			POS: r.URL.Query().Get("pos"),
		}
		if v := r.URL.Query().Get("min"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				opts.MinLength = n
			}
		}
		if v := r.URL.Query().Get("max"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				opts.MaxLength = n
			}
		}
		if v := r.URL.Query().Get("min_freq"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				opts.MinFrequency = n
			}
		}
		if v := r.URL.Query().Get("min_difficulty"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				opts.MinDifficulty = f
			}
		}
		if v := r.URL.Query().Get("max_difficulty"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				opts.MaxDifficulty = f
			}
		}

		result, err := dict.RandomWord(lang, opts)
		if err == dictionary.ErrNoMatch {
			writeError(w, 404, "no_match", "no words match the given filters")
			return
		}
		if err != nil {
			slog.Error("random", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		resp := map[string]any{"word": result.Word}
		if result.Difficulty != nil {
			resp["difficulty"] = *result.Difficulty
		}
		writeJSON(w, 200, resp)
	}
}

func handleDefine(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		defs, err := dict.Define(word, lang)
		if err != nil {
			slog.Error("define", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":        word,
			"lang":        lang,
			"definitions": defs,
		})
	}
}

func handleSynonyms(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		syns, err := dict.Synonyms(word, lang)
		if err != nil {
			slog.Error("synonyms", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":     word,
			"lang":     lang,
			"synonyms": syns,
		})
	}
}

func handleTranslate(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		if word == "" || from == "" || to == "" {
			writeError(w, 400, "bad_request", "word, from, and to are required")
			return
		}
		if !dictionary.ValidLang(from) {
			writeError(w, 400, "bad_request", "unsupported language: "+from)
			return
		}
		if !dictionary.ValidLang(to) {
			writeError(w, 400, "bad_request", "unsupported language: "+to)
			return
		}

		trans, err := dict.Translate(word, from, to)
		if err != nil {
			slog.Error("translate", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":         word,
			"from":         from,
			"to":           to,
			"translations": trans,
		})
	}
}

func handleFrequency(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		freq, err := dict.Frequency(word, lang)
		if err != nil {
			slog.Error("frequency", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":      word,
			"lang":      lang,
			"frequency": freq,
		})
	}
}

func handleFrequencyBatch(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wordsParam := r.URL.Query().Get("words")
		lang := r.URL.Query().Get("lang")
		if wordsParam == "" || lang == "" {
			writeError(w, 400, "bad_request", "words and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		words := strings.Split(wordsParam, ",")
		if len(words) > 100 {
			writeError(w, 400, "bad_request", "maximum 100 words per batch")
			return
		}

		freqs, err := dict.FrequencyBatch(words, lang)
		if err != nil {
			slog.Error("frequency batch", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"lang":        lang,
			"frequencies": freqs,
		})
	}
}

func handleAntonyms(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		ants, err := dict.Antonyms(word, lang)
		if err != nil {
			slog.Error("antonyms", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":     word,
			"lang":     lang,
			"antonyms": ants,
		})
	}
}

func handleBacking(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		equivs, err := dict.EnglishBacking(word, lang)
		if err != nil {
			slog.Error("backing", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":        word,
			"lang":        lang,
			"equivalents": equivs,
		})
	}
}

func handlePronunciation(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		prons, err := dict.Pronunciation(word, lang)
		if err != nil {
			slog.Error("pronunciation", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":           word,
			"lang":           lang,
			"pronunciations": prons,
		})
	}
}

func handleEtymology(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		etym, err := dict.Etymology(word, lang)
		if err != nil {
			slog.Error("etymology", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":      word,
			"lang":      lang,
			"etymology": etym,
		})
	}
}

func handleDifficulty(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		lang := r.URL.Query().Get("lang")
		if word == "" || lang == "" {
			writeError(w, 400, "bad_request", "word and lang are required")
			return
		}
		if !dictionary.ValidLang(lang) {
			writeError(w, 400, "bad_request", "unsupported language: "+lang)
			return
		}

		diff, err := dict.Difficulty(word, lang)
		if err != nil {
			slog.Error("difficulty", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":       word,
			"lang":       lang,
			"difficulty": diff,
		})
	}
}

func handleRhyme(dict *dictionary.Dictionary) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		word := r.URL.Query().Get("word")
		if word == "" {
			writeError(w, 400, "bad_request", "word is required")
			return
		}
		limit := 10
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}

		rhymes, err := dict.Rhymes(word, limit)
		if err != nil {
			slog.Error("rhyme", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		writeJSON(w, 200, map[string]any{
			"word":   word,
			"rhymes": rhymes,
		})
	}
}

func handleHealth(dict *dictionary.Dictionary, dbPath string) http.HandlerFunc {
	// The database is read-only at runtime, so cache the health response
	// after the first (slow) computation.
	var cached atomic.Value

	return func(w http.ResponseWriter, r *http.Request) {
		if v := cached.Load(); v != nil {
			writeJSON(w, 200, v)
			return
		}

		wordCounts, err := dict.WordCount()
		if err != nil {
			slog.Error("health: word count", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		defCounts, err := dict.DefCount()
		if err != nil {
			slog.Error("health: def count", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}

		importedAt, err := dict.Meta("imported_at")
		if err != nil {
			slog.Error("health: imported_at", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}
		schemaVersion, err := dict.Meta("schema_version")
		if err != nil {
			slog.Error("health: schema_version", "error", err)
			writeError(w, 500, "internal", "internal error")
			return
		}

		resp := map[string]any{
			"status":         "ok",
			"db_path":        dbPath,
			"word_counts":    wordCounts,
			"def_counts":     defCounts,
			"imported_at":    importedAt,
			"schema_version": schemaVersion,
		}
		cached.Store(resp)
		writeJSON(w, 200, resp)
	}
}
