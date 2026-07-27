package loader

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func queryFreqLang(t *testing.T, db *sql.DB, word, lang string) int {
	t.Helper()
	var freq int
	if err := db.QueryRow(
		"SELECT frequency FROM words WHERE word = ? AND lang = ?", word, lang,
	).Scan(&freq); err != nil {
		t.Fatalf("query frequency for %q (%s): %v", word, lang, err)
	}
	return freq
}

func TestSpanishFreq_LoadsFrequencies(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "perro", "es")
	seedWord(t, db, "cuando", "es")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "es_50k.txt"), []byte("cuando 500000\nperro 20000\n"), 0644)

	if err := (SpanishFreqLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	cuando := queryFreqLang(t, db, "cuando", "es")
	perro := queryFreqLang(t, db, "perro", "es")
	if perro == 0 {
		t.Error("expected non-zero frequency for 'perro'")
	}
	if cuando <= perro {
		t.Errorf("expected 'cuando' (%d) > 'perro' (%d)", cuando, perro)
	}
}

// Spanish and Portuguese share many spellings ("casa", "grande", "hora").
// Each frequency loader must only touch its own language's rows.
func TestSpanishFreq_DoesNotTouchOtherLanguages(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "casa", "es")
	seedWord(t, db, "casa", "pt-PT")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "es_50k.txt"), []byte("casa 100000\n"), 0644)

	if err := (SpanishFreqLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if f := queryFreqLang(t, db, "casa", "es"); f == 0 {
		t.Error("expected Spanish 'casa' to get a frequency")
	}
	if f := queryFreqLang(t, db, "casa", "pt-PT"); f != 0 {
		t.Errorf("expected Portuguese 'casa' to be untouched, got frequency %d", f)
	}
}

func TestSpanishFreq_NoFileSkips(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if err := (SpanishFreqLoader{}).Load(db, t.TempDir()); err != nil {
		t.Errorf("expected graceful skip with no file, got: %v", err)
	}
}

// The pt-PT loader must stay scoped to pt-PT now that it shares an
// implementation with the Spanish one.
func TestCETEMPublico_DoesNotTouchSpanish(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "casa", "pt-PT")
	seedWord(t, db, "casa", "es")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pt_50k.txt"), []byte("casa 100000\n"), 0644)

	if err := (CETEMPublicoLoader{}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if f := queryFreqLang(t, db, "casa", "pt-PT"); f == 0 {
		t.Error("expected Portuguese 'casa' to get a frequency")
	}
	if f := queryFreqLang(t, db, "casa", "es"); f != 0 {
		t.Errorf("expected Spanish 'casa' to be untouched, got frequency %d", f)
	}
}

func writeOMWTab(t *testing.T, dir, name, content string) {
	t.Helper()
	omwDir := filepath.Join(dir, "omw")
	if err := os.MkdirAll(omwDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(omwDir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func countSynsetLinks(t *testing.T, db *sql.DB, word, lang string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM word_synsets ws
		JOIN words w ON w.id = ws.word_id
		WHERE w.word = ? AND w.lang = ?`, word, lang).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOMW_Spanish(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "perro", "es")

	dir := t.TempDir()
	// The MCR Spanish wordnet ships pre-normalized synset IDs and prefixes
	// its relation column with the language code.
	writeOMWTab(t, dir, "wn-data-spa.tab",
		"# Multilingual Central Repository\tspa\n"+
			"02084071-n\tspa:lemma\tperro\n"+
			"02084071-n\tspa:def\tmamífero doméstico\n")

	if err := (OMWLoader{Lang: "es", Suffix: "spa"}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if n := countSynsetLinks(t, db, "perro", "es"); n != 1 {
		t.Errorf("expected 1 synset link for 'perro', got %d", n)
	}

	var synsetID, pos string
	if err := db.QueryRow("SELECT synset_id, pos FROM synsets").Scan(&synsetID, &pos); err != nil {
		t.Fatal(err)
	}
	if synsetID != "02084071-n" {
		t.Errorf("expected normalized synset ID '02084071-n', got %q", synsetID)
	}
	if pos != "noun" {
		t.Errorf("expected pos 'noun', got %q", pos)
	}
}

// A Spanish lemma must not get linked to a same-spelled Portuguese word.
func TestOMW_LangScoping(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "casa", "es")
	seedWord(t, db, "casa", "pt-PT")

	dir := t.TempDir()
	writeOMWTab(t, dir, "wn-data-spa.tab", "03544360-n\tspa:lemma\tcasa\n")

	if err := (OMWLoader{Lang: "es", Suffix: "spa"}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	if n := countSynsetLinks(t, db, "casa", "es"); n != 1 {
		t.Errorf("expected 1 synset link for Spanish 'casa', got %d", n)
	}
	if n := countSynsetLinks(t, db, "casa", "pt-PT"); n != 0 {
		t.Errorf("expected no synset links for Portuguese 'casa', got %d", n)
	}
}

// Portuguese (OpenWN-PT) and Spanish (MCR) label the relation column
// differently; both must be recognized, and non-lemma rows must not be.
func TestOMW_LemmaRelationForms(t *testing.T) {
	for _, rel := range []string{"lemma", "spa:lemma", "por:lemma"} {
		if !isOMWLemmaRelation(rel) {
			t.Errorf("isOMWLemmaRelation(%q) = false, want true", rel)
		}
	}
	for _, rel := range []string{"def", "spa:def", "spa:exe", "spa", ""} {
		if isOMWLemmaRelation(rel) {
			t.Errorf("isOMWLemmaRelation(%q) = true, want false", rel)
		}
	}
}

// The legacy "eng-30-XXXXXXXX-n" synset form must keep working alongside
// the pre-normalized IDs the current OMW releases ship.
func TestOMW_LegacySynsetIDForm(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "gato", "es")

	dir := t.TempDir()
	writeOMWTab(t, dir, "wn-data-spa.tab", "eng-30-02121620-n\tlemma\tgato\n")

	if err := (OMWLoader{Lang: "es", Suffix: "spa"}).Load(db, dir); err != nil {
		t.Fatal(err)
	}

	var synsetID string
	if err := db.QueryRow("SELECT synset_id FROM synsets").Scan(&synsetID); err != nil {
		t.Fatal(err)
	}
	if synsetID != "02121620-n" {
		t.Errorf("expected normalized synset ID '02121620-n', got %q", synsetID)
	}
	if n := countSynsetLinks(t, db, "gato", "es"); n != 1 {
		t.Errorf("expected 1 synset link for 'gato', got %d", n)
	}
}

func TestOMW_NameIncludesLang(t *testing.T) {
	if got := (OMWLoader{Lang: "es", Suffix: "spa"}).Name(); got != "omw-es" {
		t.Errorf("expected loader name 'omw-es', got %q", got)
	}
	// pt-PT keeps its historical loader name so --skip omw-pt still works.
	if got := (OMWLoader{Lang: "pt-PT", Suffix: "por", LangName: "pt"}).Name(); got != "omw-pt" {
		t.Errorf("expected loader name 'omw-pt', got %q", got)
	}
}

func TestOMW_NoFileSkips(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	if err := (OMWLoader{Lang: "es", Suffix: "spa"}).Load(db, t.TempDir()); err != nil {
		t.Errorf("expected graceful skip with no file, got: %v", err)
	}
}

func TestWiktionary_Spanish(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "perro", "es")

	dir := t.TempDir()
	jsonl := `{"word":"perro","pos":"noun","lang_code":"es","senses":[{"glosses":["mamífero doméstico de la familia de los cánidos"],"tags":[],"synonyms":[{"word":"can"}]}],"translations":[{"code":"en","word":"dog"},{"code":"fr","word":"chien"}],"sounds":[{"ipa":"ˈpe.ro"}],"etymology_text":"Del latín petrus, de origen incierto."}` + "\n"
	path := writeJSONL(t, dir, "kaikki-es.jsonl", jsonl)

	if err := loadWiktionary(db, path, "es"); err != nil {
		t.Fatal(err)
	}

	var gloss string
	if err := db.QueryRow(`
		SELECT d.gloss FROM definitions d
		JOIN words w ON w.id = d.word_id
		WHERE w.word = 'perro' AND w.lang = 'es'`).Scan(&gloss); err != nil {
		t.Fatal(err)
	}
	if gloss == "" {
		t.Error("expected a Spanish definition for 'perro'")
	}

	var syn string
	if err := db.QueryRow("SELECT synonym FROM synonyms").Scan(&syn); err != nil {
		t.Fatal(err)
	}
	if syn != "can" {
		t.Errorf("expected synonym 'can', got %q", syn)
	}

	// Translations out of Spanish should resolve to supported target languages.
	var enTrans string
	if err := db.QueryRow(
		"SELECT translation FROM translations WHERE target_lang = 'en'").Scan(&enTrans); err != nil {
		t.Fatal(err)
	}
	if enTrans != "dog" {
		t.Errorf("expected en translation 'dog', got %q", enTrans)
	}

	var ipa string
	if err := db.QueryRow("SELECT value FROM pronunciations WHERE format = 'ipa'").Scan(&ipa); err != nil {
		t.Fatal(err)
	}
	if ipa != "ˈpe.ro" {
		t.Errorf("expected IPA 'ˈpe.ro', got %q", ipa)
	}

	var etym string
	if err := db.QueryRow("SELECT text FROM etymology").Scan(&etym); err != nil {
		t.Fatal(err)
	}
	if etym == "" {
		t.Error("expected an etymology for 'perro'")
	}
}

// English Wiktionary entries carry Spanish translations; "es" must be a
// recognized target language code or they get dropped.
func TestWiktionary_EnglishToSpanishTranslation(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	seedWord(t, db, "house", "en")

	dir := t.TempDir()
	jsonl := `{"word":"house","pos":"noun","lang_code":"en","senses":[{"glosses":["a building for people to live in"],"tags":[]}],"translations":[{"code":"es","word":"casa"}]}` + "\n"
	path := writeJSONL(t, dir, "kaikki-en.jsonl", jsonl)

	if err := loadWiktionary(db, path, "en"); err != nil {
		t.Fatal(err)
	}

	var trans string
	if err := db.QueryRow(
		"SELECT translation FROM translations WHERE target_lang = 'es'").Scan(&trans); err != nil {
		t.Fatalf("expected an es translation for 'house': %v", err)
	}
	if trans != "casa" {
		t.Errorf("expected 'casa', got %q", trans)
	}
}
