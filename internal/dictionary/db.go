package dictionary

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

const schemaVersion = "2"

const schemaSQL = `
CREATE TABLE IF NOT EXISTS words (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    word      TEXT    NOT NULL,
    lang      TEXT    NOT NULL,
    pos       TEXT,
    frequency  INTEGER DEFAULT 0,
    difficulty REAL DEFAULT NULL,
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
CREATE INDEX IF NOT EXISTS idx_translations_reverse ON translations(translation, target_lang);

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS antonyms (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    antonym  TEXT    NOT NULL,
    source   TEXT    NOT NULL,
    UNIQUE(word_id, antonym)
);

CREATE INDEX IF NOT EXISTS idx_antonyms_word_id ON antonyms(word_id);

CREATE TABLE IF NOT EXISTS synsets (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    synset_id TEXT NOT NULL UNIQUE,
    pos       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS word_synsets (
    word_id   INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    synset_id INTEGER NOT NULL REFERENCES synsets(id) ON DELETE CASCADE,
    source    TEXT NOT NULL,
    PRIMARY KEY (word_id, synset_id)
);

CREATE INDEX IF NOT EXISTS idx_word_synsets_word_id   ON word_synsets(word_id);
CREATE INDEX IF NOT EXISTS idx_word_synsets_synset_id ON word_synsets(synset_id);

CREATE TABLE IF NOT EXISTS pronunciations (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    format   TEXT NOT NULL,
    value    TEXT NOT NULL,
    source   TEXT NOT NULL,
    UNIQUE(word_id, format, source)
);

CREATE INDEX IF NOT EXISTS idx_pronunciations_word_id ON pronunciations(word_id);

CREATE TABLE IF NOT EXISTS etymology (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id    INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    source     TEXT NOT NULL,
    UNIQUE(word_id, source)
);

CREATE INDEX IF NOT EXISTS idx_etymology_word_id ON etymology(word_id);
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
	db, err := sql.Open("sqlite", dbPath+
		"?mode=ro"+
		"&_pragma=journal_mode(wal)"+
		"&_pragma=foreign_keys(on)"+
		"&_pragma=cache_size(-64000)"+
		"&_pragma=mmap_size(268435456)"+
		"&_pragma=temp_store(memory)")
	if err != nil {
		return nil, fmt.Errorf("dictionary: open db (ro): %w", err)
	}
	// Pin to one connection so pragmas are consistent. WAL mode still
	// allows concurrent reads from the same connection in Go's model.
	db.SetMaxOpenConns(1)
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

	// Schema migrations for databases created before v2.
	// ALTER TABLE ADD COLUMN is a no-op if the column already exists in SQLite
	// when we catch the "duplicate column" error.
	migrations := []string{
		"ALTER TABLE words ADD COLUMN difficulty REAL DEFAULT NULL",
		"ALTER TABLE pronunciations ADD COLUMN cmu_tail TEXT",
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			// Ignore "duplicate column name" — means migration already applied
			if !isDuplicateColumn(err) {
				return fmt.Errorf("dictionary: migration: %w", err)
			}
		}
	}

	return nil
}

// Migrate opens the database read-write, applies any pending schema migrations,
// and populates derived data (e.g. CMU rhyme tails). Returns the number of
// CMU tails populated (0 if already up to date).
func Migrate(dbPath string) (int, error) {
	db, err := openDB(dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	if err := BootstrapSchema(db); err != nil {
		return 0, err
	}

	return PopulateCMUTails(db)
}

func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column")
}

// PopulateCMUTails computes the rhyme tail for all CMU pronunciation entries
// that don't already have one. The tail is the phoneme sequence from the last
// stressed vowel onward (e.g. "K AE1 T" → "AE1 T").
func PopulateCMUTails(db *sql.DB) (int, error) {
	// Create index if it doesn't exist.
	db.Exec("CREATE INDEX IF NOT EXISTS idx_pronunciations_cmu_tail ON pronunciations(cmu_tail)")

	rows, err := db.Query(
		`SELECT id, value FROM pronunciations WHERE format = 'cmu' AND (cmu_tail IS NULL OR cmu_tail = '')`)
	if err != nil {
		return 0, fmt.Errorf("dictionary: populate cmu tails: %w", err)
	}

	type entry struct {
		id    int
		value string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.value); err != nil {
			rows.Close()
			return 0, fmt.Errorf("dictionary: populate cmu tails scan: %w", err)
		}
		entries = append(entries, e)
	}
	rows.Close()

	if len(entries) == 0 {
		return 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("dictionary: populate cmu tails begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE pronunciations SET cmu_tail = ? WHERE id = ?")
	if err != nil {
		return 0, fmt.Errorf("dictionary: populate cmu tails prepare: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, e := range entries {
		tail := cmuTail(e.value)
		if tail == "" {
			continue
		}
		if _, err := stmt.Exec(tail, e.id); err != nil {
			return 0, fmt.Errorf("dictionary: populate cmu tails update: %w", err)
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("dictionary: populate cmu tails commit: %w", err)
	}
	return count, nil
}

// cmuTail extracts the phoneme sequence from the last stressed vowel onward.
// e.g. "K AE1 T" → "AE1 T", "IH0 F EH1 M ER0 AH0 L" → "EH1 M ER0 AH0 L"
func cmuTail(phonemes string) string {
	parts := strings.Fields(phonemes)
	lastStressed := -1
	for i, p := range parts {
		if len(p) >= 2 && p[len(p)-1] == '1' {
			lastStressed = i
		}
	}
	// Fall back to last vowel with any stress marker.
	if lastStressed == -1 {
		for i, p := range parts {
			if len(p) >= 2 && (p[len(p)-1] == '0' || p[len(p)-1] == '2') {
				lastStressed = i
			}
		}
	}
	if lastStressed == -1 {
		return ""
	}
	return strings.Join(parts[lastStressed:], " ")
}
