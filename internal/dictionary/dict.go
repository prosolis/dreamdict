package dictionary

import (
	"database/sql"
	"errors"
)

var (
	ErrNoMatch   = errors.New("dictionary: no word matches filters")
	ErrNotSeeded = errors.New("dictionary: database not seeded, run: go run ./cmd/dictimport")
)

var supportedLangs = []string{"en", "fr", "pt-PT", "zh"}

func ValidLang(lang string) bool {
	for _, l := range supportedLangs {
		if l == lang {
			return true
		}
	}
	return false
}

type Options struct {
	MinLength    int
	MaxLength    int
	POS          string // "", "noun", "verb", "adjective", "adverb"
	MinFrequency int    // 0 = no filter
}

type Definition struct {
	POS      string `json:"pos"`
	Gloss    string `json:"gloss"`
	Source   string `json:"source"`
	Priority int    `json:"priority"`
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
