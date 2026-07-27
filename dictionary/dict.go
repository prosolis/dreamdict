// Package dictionary is DreamDict's query layer over a built dict.db: the
// schema, the types, and every lookup the HTTP server exposes.
//
// It sits outside internal/ deliberately, because the server is not the only
// way to consume DreamDict. A Go program that already has the database file on
// disk can skip the network entirely — open it with NewReadOnly and call the
// same methods the handlers call. The loaders that *build* the database stay
// internal; only reading it is public API.
package dictionary

import (
	"database/sql"
	"errors"
)

var (
	ErrNoMatch   = errors.New("dictionary: no word matches filters")
	ErrNotSeeded = errors.New("dictionary: database not seeded, run: go run ./cmd/dictimport")
)

var supportedLangs = []string{"en", "fr", "pt-PT", "es", "zh"}

// Langs returns every supported language code. Import and reporting code
// iterates this rather than keeping its own list, so adding a language is a
// one-line change here.
func Langs() []string {
	return append([]string(nil), supportedLangs...)
}

func ValidLang(lang string) bool {
	for _, l := range supportedLangs {
		if l == lang {
			return true
		}
	}
	return false
}

type Options struct {
	MinLength     int
	MaxLength     int
	POS           string  // "", "noun", "verb", "adjective", "adverb"
	MinFrequency  int     // 0 = no filter
	MinDifficulty float64 // 0.0 = no filter
	MaxDifficulty float64 // 0.0 = no filter
	Variant       string  // "", "us", "gb" — empty means no filter
}

type Definition struct {
	POS      string `json:"pos"`
	Gloss    string `json:"gloss"`
	Source   string `json:"source"`
	Priority int    `json:"priority"`
}

type EnglishEquivalent struct {
	Word       string `json:"word"`
	Definition string `json:"definition"`
	Synset     string `json:"synset"`
}

type Pronunciation struct {
	Format string `json:"format"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type Dictionary struct {
	db *sql.DB
}

// New opens the database read-write (for import CLI).
func New(dbPath string) (*Dictionary, error) {
	db, err := openDB(dbPath)
	if err != nil {
		return nil, err
	}
	d := &Dictionary{db: db}
	if err := d.checkSeeded(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// NewReadOnly opens the database read-only with concurrent read support (for server).
func NewReadOnly(dbPath string) (*Dictionary, error) {
	db, err := openReadOnlyDB(dbPath)
	if err != nil {
		return nil, err
	}
	d := &Dictionary{db: db}
	if err := d.checkSeeded(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// NewFromDB creates a Dictionary from an existing *sql.DB (useful for testing).
func NewFromDB(db *sql.DB) *Dictionary {
	return &Dictionary{db: db}
}

func (d *Dictionary) Close() error {
	return d.db.Close()
}

func (d *Dictionary) checkSeeded() error {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM meta").Scan(&count)
	if err != nil {
		return ErrNotSeeded
	}
	if count == 0 {
		return ErrNotSeeded
	}
	return nil
}

func (d *Dictionary) SupportedLangs() []string {
	return supportedLangs
}
