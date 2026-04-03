package loader

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func seedWord(t *testing.T, db *sql.DB, word, lang string) {
	t.Helper()
	if _, err := db.Exec("INSERT INTO words (word, lang) VALUES (?, ?)", word, lang); err != nil {
		t.Fatal(err)
	}
}

func queryFreq(t *testing.T, db *sql.DB, word string) int {
	t.Helper()
	var freq int
	if err := db.QueryRow("SELECT frequency FROM words WHERE word = ?", word).Scan(&freq); err != nil {
		t.Fatalf("query frequency for %q: %v", word, err)
	}
	return freq
}

func TestCETEMPublico_WordSpaceFormat(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "casa", "pt-PT")
	seedWord(t, db, "homem", "pt-PT")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pt_50k.txt"), []byte("casa 50000\nhomem 30000\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if f := queryFreq(t, db, "casa"); f == 0 {
		t.Error("expected non-zero frequency for 'casa'")
	}
	if f := queryFreq(t, db, "homem"); f == 0 {
		t.Error("expected non-zero frequency for 'homem'")
	}
}

func TestCETEMPublico_ReversedColumns(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "grande", "pt-PT")

	dir := t.TempDir()
	// CETEMPúblico format: count<tab>word
	os.WriteFile(filepath.Join(dir, "cetempublico-freq.tsv"), []byte("500000\tgrande\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if f := queryFreq(t, db, "grande"); f == 0 {
		t.Error("expected non-zero frequency for 'grande' with reversed columns")
	}
}

func TestCETEMPublico_InsertsMissingWords(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pt_50k.txt"), []byte("que 5000000\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	// "que" was not seeded — loader should have inserted it
	if f := queryFreq(t, db, "que"); f == 0 {
		t.Error("expected non-zero frequency for auto-inserted word 'que'")
	}
}

func TestCETEMPublico_ISO88591(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "não", "pt-PT")

	dir := t.TempDir()
	// ISO-8859-1: "5000\tnão\n" where ã = 0xE3 (single byte)
	os.WriteFile(filepath.Join(dir, "cetempublico-freq.tsv"),
		[]byte{0x35, 0x30, 0x30, 0x30, 0x09, 0x6E, 0xE3, 0x6F, 0x0A}, 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if f := queryFreq(t, db, "não"); f == 0 {
		t.Error("expected non-zero frequency for ISO-8859-1 encoded 'não'")
	}
}

func TestCETEMPublico_FiltersJunk(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pt_50k.txt"),
		[]byte("word1 5000\n. 90000\n, 80000\nboa 3000\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM words WHERE lang = 'pt-PT'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 word (boa only), got %d", count)
	}
}

func TestCETEMPublico_NoFileSkips(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if err := (CETEMPublicoLoader{}).Load(db, t.TempDir()); err != nil {
		t.Errorf("expected graceful skip with no file, got: %v", err)
	}
}

func TestCETEMPublico_FrequencyScaling(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "raro", "pt-PT")
	seedWord(t, db, "comum", "pt-PT")

	dir := t.TempDir()
	// "comum" has much higher frequency than "raro"
	os.WriteFile(filepath.Join(dir, "pt_50k.txt"),
		[]byte("comum 5000000\nraro 50\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	comum := queryFreq(t, db, "comum")
	raro := queryFreq(t, db, "raro")

	if comum <= raro {
		t.Errorf("expected 'comum' (%d) > 'raro' (%d)", comum, raro)
	}
	if comum > 10000 {
		t.Errorf("frequency should cap at 10000, got %d", comum)
	}
}
