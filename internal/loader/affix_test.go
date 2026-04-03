package loader

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandWord_SuffixRule(t *testing.T) {
	rules := map[string]*affixRuleSet{
		"S": {
			ruleType: "SFX",
			rules: []affixRule{
				{ruleType: "SFX", strip: "", affix: "s", matchers: nil},
			},
		},
	}

	forms := expandWord("cat", "S", rules)
	if len(forms) != 1 || forms[0] != "cats" {
		t.Errorf("expected [cats], got %v", forms)
	}
}

func TestExpandWord_StripAndReplace(t *testing.T) {
	rules := map[string]*affixRuleSet{
		"D": {
			ruleType: "SFX",
			rules: []affixRule{
				{ruleType: "SFX", strip: "e", affix: "ing", matchers: parseConditionMatchers("e")},
			},
		},
	}

	forms := expandWord("make", "D", rules)
	if len(forms) != 1 || forms[0] != "making" {
		t.Errorf("expected [making], got %v", forms)
	}

	// Word not matching condition should produce no forms
	forms2 := expandWord("run", "D", rules)
	if len(forms2) != 0 {
		t.Errorf("expected no forms for 'run' with 'e' condition, got %v", forms2)
	}
}

func TestExpandWord_PrefixRule(t *testing.T) {
	rules := map[string]*affixRuleSet{
		"U": {
			ruleType: "PFX",
			rules: []affixRule{
				{ruleType: "PFX", strip: "", affix: "un", matchers: nil},
			},
		},
	}

	forms := expandWord("happy", "U", rules)
	if len(forms) != 1 || forms[0] != "unhappy" {
		t.Errorf("expected [unhappy], got %v", forms)
	}
}

func TestExpandWord_MaxCap(t *testing.T) {
	// Create a rule that generates many forms
	rules := map[string]*affixRuleSet{
		"X": {
			ruleType: "SFX",
			rules:    make([]affixRule, 100),
		},
	}
	for i := range rules["X"].rules {
		rules["X"].rules[i] = affixRule{ruleType: "SFX", strip: "", affix: string(rune('a' + i%26)) + string(rune('0'+i/26))}
	}

	forms := expandWord("test", "X", rules)
	if len(forms) > maxFormsPerWord {
		t.Errorf("expected at most %d forms, got %d", maxFormsPerWord, len(forms))
	}
}

func TestParseAffFile(t *testing.T) {
	dir := t.TempDir()
	affContent := `SET UTF-8

SFX S Y 1
SFX S 0 s .

PFX U Y 1
PFX U 0 un .
`
	affPath := filepath.Join(dir, "test.aff")
	os.WriteFile(affPath, []byte(affContent), 0644)

	rules, err := parseAffFile(affPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := rules["S"]; !ok {
		t.Error("expected SFX rule 'S'")
	}
	if _, ok := rules["U"]; !ok {
		t.Error("expected PFX rule 'U'")
	}
}

func TestParseDicFile(t *testing.T) {
	dir := t.TempDir()
	dicContent := "3\ncat/S\ndog/S\nhappy/U\n"
	dicPath := filepath.Join(dir, "test.dic")
	os.WriteFile(dicPath, []byte(dicContent), 0644)

	entries, err := parseDicFile(dicPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].word != "cat" || entries[0].flags != "S" {
		t.Errorf("expected cat/S, got %s/%s", entries[0].word, entries[0].flags)
	}
}

func TestAffixLoader_NoFileSkips(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	loader := AffixLoader{Lang: "en", DicFile: "nonexistent.dic", AffFile: "nonexistent.aff"}
	if err := loader.Load(db, t.TempDir()); err != nil {
		t.Errorf("expected graceful skip, got: %v", err)
	}
}

func TestAffixLoader_EndToEnd(t *testing.T) {
	db := setupWiktTestDB(t)
	defer db.Close()

	dir := t.TempDir()

	affContent := `SET UTF-8

SFX S Y 1
SFX S 0 s .
`
	os.WriteFile(filepath.Join(dir, "test.aff"), []byte(affContent), 0644)

	dicContent := "2\ncat/S\ndog/S\n"
	os.WriteFile(filepath.Join(dir, "test.dic"), []byte(dicContent), 0644)

	loader := AffixLoader{Lang: "en", DicFile: "test.dic", AffFile: "test.aff"}
	if err := loader.Load(db, dir); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM words WHERE lang = 'en'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 expanded forms (cats, dogs), got %d", count)
	}

	// Verify specific forms exist
	var exists bool
	db.QueryRow("SELECT EXISTS(SELECT 1 FROM words WHERE word = 'cats' AND lang = 'en')").Scan(&exists)
	if !exists {
		t.Error("expected 'cats' to exist")
	}
}
