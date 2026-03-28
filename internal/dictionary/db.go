package dictionary

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schemaVersion = "1"

const schemaSQL = `
CREATE TABLE IF NOT EXISTS words (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    word      TEXT    NOT NULL,
    lang      TEXT    NOT NULL,
    pos       TEXT,
    frequency INTEGER DEFAULT 0,
    UNIQUE(word, lang)
);

CREATE INDEX IF NOT EXISTS idx_words_lang     ON words(lang);
CREATE INDEX IF NOT EXISTS idx_words_lang_pos ON words(lang, pos);
CREATE INDEX IF NOT EXISTS idx_words_word     ON words(word);

CREATE TABLE IF NOT EXISTS definitions (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    pos      TEXT,
    gloss    TEXT    NOT NULL,
    source   TEXT    NOT NULL,
    priority INTEGER NOT NULL DEFAULT 99,
    UNIQUE(word_id, gloss)
);

CREATE INDEX IF NOT EXISTS idx_definitions_word_id          ON definitions(word_id);
CREATE INDEX IF NOT EXISTS idx_definitions_word_id_priority ON definitions(word_id, priority);

CREATE TABLE IF NOT EXISTS synonyms (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    synonym  TEXT    NOT NULL,
    source   TEXT    NOT NULL,
    UNIQUE(word_id, synonym)
);

CREATE INDEX IF NOT EXISTS idx_synonyms_word_id ON synonyms(word_id);

CREATE TABLE IF NOT EXISTS translations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id     INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    translation TEXT    NOT NULL,
    target_lang TEXT    NOT NULL,
    source      TEXT    NOT NULL,
    UNIQUE(word_id, translation, target_lang)
);

CREATE INDEX IF NOT EXISTS idx_translations_word_id ON translations(word_id);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

func openDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("dictionary: open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite write serialization for import
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("dictionary: ping db: %w", err)
	}
	return db, nil
}

func openReadOnlyDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("dictionary: open db (ro): %w", err)
	}
	// No MaxOpenConns limit — WAL mode supports concurrent reads
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("dictionary: ping db: %w", err)
	}
	return db, nil
}

func BootstrapSchema(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("dictionary: bootstrap schema: %w", err)
	}
	return nil
}
