package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"dreamdict/internal/dictionary"
	"dreamdict/internal/loader"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "./dict.db", "Path to output dict.db")
	dataDir := flag.String("data", "./data", "Path to data directory")
	langs := flag.String("langs", "en,fr,pt-PT,zh", "Comma-separated languages to import")
	clean := flag.Bool("clean", false, "Drop and recreate all tables before import")
	skipFlag := flag.String("skip", "", "Comma-separated loader names to skip")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	db, err := sql.Open("sqlite", *dbPath+
		"?_pragma=journal_mode(wal)"+
		"&_pragma=foreign_keys(on)"+
		"&_pragma=synchronous(off)"+
		"&_pragma=cache_size(-64000)"+
		"&_pragma=mmap_size(268435456)"+
		"&_pragma=temp_store(memory)")
	if err != nil {
		slog.Error("open db", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if *clean {
		slog.Info("dropping all tables")
		for _, table := range []string{"etymology", "pronunciations", "word_synsets", "synsets", "antonyms", "translations", "synonyms", "definitions", "words", "meta"} {
			if _, err := db.Exec("DROP TABLE IF EXISTS " + table); err != nil {
				slog.Error("drop table", "table", table, "error", err)
				os.Exit(1)
			}
		}
	}

	if err := dictionary.BootstrapSchema(db); err != nil {
		slog.Error("bootstrap schema", "error", err)
		os.Exit(1)
	}

	langSet := make(map[string]bool)
	for _, l := range strings.Split(*langs, ",") {
		langSet[strings.TrimSpace(l)] = true
	}

	skipSet := make(map[string]bool)
	if *skipFlag != "" {
		for _, s := range strings.Split(*skipFlag, ",") {
			skipSet[strings.TrimSpace(s)] = true
		}
	}

	var loaders []loader.Loader

	if langSet["en"] {
		loaders = append(loaders,
			loader.SCOWLLoader{},
			loader.EnglishAffixLoader{},
			loader.WordNetLoader{},
			loader.SubtlexLoader{},
			loader.CMUDictLoader{},
			loader.WiktionaryLoader{Lang: "en", FileName: "kaikki-en.jsonl"},
		)
	}

	if langSet["fr"] {
		loaders = append(loaders,
			loader.HunspellLoader{Lang: "fr", FileName: "fr_FR/fr.dic"},
			loader.AffixLoader{Lang: "fr", DicFile: "fr_FR/fr.dic", AffFile: "fr_FR/fr.aff"},
			loader.LexiqueLoader{},
			loader.WOLFLoader{},
			loader.WiktionaryLoader{Lang: "fr", FileName: "kaikki-fr.jsonl"},
		)
	}

	if langSet["pt-PT"] {
		loaders = append(loaders,
			loader.HunspellLoader{Lang: "pt-PT", FileName: "pt_PT/pt_PT.dic"},
			loader.AffixLoader{Lang: "pt-PT", DicFile: "pt_PT/pt_PT.dic", AffFile: "pt_PT/pt_PT.aff"},
			loader.DicionarioLoader{},
			loader.CETEMPublicoLoader{},
			loader.OMWLoader{},
			loader.WiktionaryLoader{Lang: "pt-PT", FileName: "kaikki-pt.jsonl"},
		)
	}

	if langSet["zh"] {
		loaders = append(loaders,
			loader.CEDICTLoader{},
			loader.WiktionaryLoader{Lang: "zh", FileName: "kaikki-zh.jsonl"},
		)
	}

	// Difficulty scorer runs last, after all word data is loaded
	loaders = append(loaders, loader.DifficultyScorer{})

	start := time.Now()
	if err := loader.Run(db, *dataDir, loaders, skipSet); err != nil {
		slog.Error("import failed", "error", err)
		os.Exit(1)
	}

	// Write meta
	now := time.Now().UTC().Format(time.RFC3339)
	for _, kv := range [][2]string{
		{"imported_at", now},
		{"schema_version", "2"},
	} {
		if _, err := db.Exec(
			"INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", kv[0], kv[1],
		); err != nil {
			slog.Error("write meta", "error", err)
			os.Exit(1)
		}
	}

	// Print summary
	dict := dictionary.NewFromDB(db)
	wordCounts, _ := dict.WordCount()
	defCounts, _ := dict.DefCount()

	fmt.Println("\n=== Import Summary ===")
	fmt.Printf("Elapsed: %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Println("Word counts:")
	for _, lang := range []string{"en", "fr", "pt-PT", "zh"} {
		if c, ok := wordCounts[lang]; ok {
			fmt.Printf("  %s: %d\n", lang, c)
		}
	}
	fmt.Println("Definition counts:")
	for _, lang := range []string{"en", "fr", "pt-PT", "zh"} {
		if c, ok := defCounts[lang]; ok {
			fmt.Printf("  %s: %d\n", lang, c)
		}
	}
}
