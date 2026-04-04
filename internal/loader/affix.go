package loader

import (
	"bufio"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AffixLoader expands inflected forms from Hunspell .aff/.dic pairs
// and inserts them into the words table alongside base forms.
type AffixLoader struct {
	Lang    string // "en", "fr", "pt-PT"
	DicFile string // path relative to dataDir, e.g. "fr_FR/fr.dic"
	AffFile string // path relative to dataDir, e.g. "fr_FR/fr.aff"
}

func (a AffixLoader) Name() string { return "affix-" + a.Lang }

func (a AffixLoader) Load(db *sql.DB, dataDir string) error {
	affPath := filepath.Join(dataDir, a.AffFile)
	dicPath := filepath.Join(dataDir, a.DicFile)

	if _, err := os.Stat(affPath); os.IsNotExist(err) {
		slog.Warn("affix: .aff file not found, skipping", "path", affPath)
		return nil
	}
	if _, err := os.Stat(dicPath); os.IsNotExist(err) {
		slog.Warn("affix: .dic file not found, skipping", "path", dicPath)
		return nil
	}

	rules, err := parseAffFile(affPath)
	if err != nil {
		return fmt.Errorf("affix: parse aff: %w", err)
	}

	dicEntries, err := parseDicFile(dicPath)
	if err != nil {
		return fmt.Errorf("affix: parse dic: %w", err)
	}

	var forms []string
	seen := make(map[string]struct{})
	for _, entry := range dicEntries {
		for _, form := range expandWord(entry.word, entry.flags, rules) {
			if !hasLetter(form) || containsDigit(form) || containsSpace(form) {
				continue
			}
			if _, dup := seen[form]; dup {
				continue
			}
			seen[form] = struct{}{}
			forms = append(forms, form)
		}
	}

	if err := bulkInsertWords(db, forms, a.Lang, 250); err != nil {
		return fmt.Errorf("affix: %w", err)
	}
	slog.Info("affix loaded", "lang", a.Lang, "expanded_forms", len(forms))
	return nil
}

type affixRule struct {
	ruleType  string // "SFX" or "PFX"
	strip     string
	affix     string
	matchers  []condMatcher // pre-parsed condition
}

type affixRuleSet struct {
	rules      []affixRule
	combinable bool
	ruleType   string
}

type dicEntry struct {
	word  string
	flags string
}

func parseAffFile(path string) (map[string]*affixRuleSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rules := make(map[string]*affixRuleSet)
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		ruleType := fields[0]
		if ruleType != "SFX" && ruleType != "PFX" {
			continue
		}

		flag := fields[1]

		// Header line: SFX flag combinable count
		if _, err := strconv.Atoi(fields[len(fields)-1]); err == nil && len(fields) == 4 {
			combinable := fields[2] == "Y"
			if _, exists := rules[flag]; !exists {
				rules[flag] = &affixRuleSet{
					combinable: combinable,
					ruleType:   ruleType,
				}
			}
			continue
		}

		// Rule line: SFX flag strip affix [condition]
		if len(fields) < 4 {
			continue
		}

		strip := fields[2]
		if strip == "0" {
			strip = ""
		}
		affix := fields[3]
		if affix == "0" {
			affix = ""
		}

		// Strip any continuation flags from affix (e.g., "ing/S" -> "ing")
		if slashIdx := strings.Index(affix, "/"); slashIdx != -1 {
			affix = affix[:slashIdx]
		}

		condition := "."
		if len(fields) >= 5 {
			condition = fields[4]
		}

		rs, exists := rules[flag]
		if !exists {
			rs = &affixRuleSet{ruleType: ruleType}
			rules[flag] = rs
		}

		rs.rules = append(rs.rules, affixRule{
			ruleType: ruleType,
			strip:    strip,
			affix:    affix,
			matchers: parseConditionMatchers(condition),
		})
	}

	return rules, scanner.Err()
}

func parseDicFile(path string) ([]dicEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Skip first line (word count)
	if scanner.Scan() {
		// discard
	}

	var entries []dicEntry
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "/", 2)
		word := strings.ToLower(parts[0])
		flags := ""
		if len(parts) > 1 {
			flags = parts[1]
		}

		if containsDigit(word) || containsNonLatin(word) {
			continue
		}

		entries = append(entries, dicEntry{word: word, flags: flags})
	}

	return entries, scanner.Err()
}

// maxFormsPerWord caps the number of inflected forms generated per base word.
// Portuguese verbs can have 60+ valid conjugations (gerunds, subjunctives, etc.),
// so the cap must be generous enough to avoid cutting off common forms.
const maxFormsPerWord = 80

func expandWord(word, flags string, rules map[string]*affixRuleSet) []string {
	seen := map[string]bool{word: true} // base form already in DB
	var result []string

	// Apply single-flag suffix/prefix rules
	for _, r := range flags {
		rs, ok := rules[string(r)]
		if !ok {
			continue
		}
		for _, rule := range rs.rules {
			if len(result) >= maxFormsPerWord {
				return result
			}
			form := applyRule(word, rule)
			if form != "" && !seen[form] {
				seen[form] = true
				result = append(result, form)
			}
		}
	}

	return result
}

func applyRule(word string, rule affixRule) string {
	if !matchCondition(word, rule.matchers, rule.ruleType) {
		return ""
	}

	switch rule.ruleType {
	case "SFX":
		base := word
		if rule.strip != "" {
			if !strings.HasSuffix(base, rule.strip) {
				return ""
			}
			base = base[:len(base)-len(rule.strip)]
		}
		return base + rule.affix

	case "PFX":
		base := word
		if rule.strip != "" {
			if !strings.HasPrefix(base, rule.strip) {
				return ""
			}
			base = base[len(rule.strip):]
		}
		return rule.affix + base
	}

	return ""
}

func matchCondition(word string, matchers []condMatcher, ruleType string) bool {
	if len(matchers) == 0 {
		return true
	}

	wordRunes := []rune(word)
	if len(wordRunes) < len(matchers) {
		return false
	}

	if ruleType == "SFX" {
		start := len(wordRunes) - len(matchers)
		for i, m := range matchers {
			if !m.matches(wordRunes[start+i]) {
				return false
			}
		}
	} else {
		for i, m := range matchers {
			if !m.matches(wordRunes[i]) {
				return false
			}
		}
	}
	return true
}

type condMatcher struct {
	chars  []rune
	negate bool
}

func (m condMatcher) matches(r rune) bool {
	if len(m.chars) == 0 {
		return true // "." wildcard
	}
	for _, c := range m.chars {
		if c == r {
			return !m.negate
		}
	}
	return m.negate
}

func parseConditionMatchers(condition string) []condMatcher {
	var matchers []condMatcher
	runes := []rune(condition)
	i := 0
	for i < len(runes) {
		if runes[i] == '[' {
			i++
			negate := false
			if i < len(runes) && runes[i] == '^' {
				negate = true
				i++
			}
			var chars []rune
			for i < len(runes) && runes[i] != ']' {
				chars = append(chars, runes[i])
				i++
			}
			if i < len(runes) {
				i++ // skip ']'
			}
			matchers = append(matchers, condMatcher{chars: chars, negate: negate})
		} else if runes[i] == '.' {
			matchers = append(matchers, condMatcher{}) // wildcard
			i++
		} else {
			matchers = append(matchers, condMatcher{chars: []rune{runes[i]}})
			i++
		}
	}
	return matchers
}

// EnglishAffixLoader generates common English inflected forms from base words
// already in the database. SCOWL doesn't ship .aff rules, so this uses
// programmatic English morphology rules instead.
type EnglishAffixLoader struct{}

func (EnglishAffixLoader) Name() string { return "affix-en" }

func (EnglishAffixLoader) Load(db *sql.DB, dataDir string) error {
	// Read all base words first, then close the query before writing.
	rows, err := db.Query("SELECT word FROM words WHERE lang = 'en'")
	if err != nil {
		return fmt.Errorf("affix-en: query words: %w", err)
	}

	var baseWords []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			rows.Close()
			return fmt.Errorf("affix-en: scan: %w", err)
		}
		baseWords = append(baseWords, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("affix-en: rows err: %w", err)
	}

	// Generate all forms in memory first — avoids per-word DB round trips
	// during generation. Deduplicate against base words.
	baseSet := make(map[string]struct{}, len(baseWords))
	for _, w := range baseWords {
		baseSet[w] = struct{}{}
	}

	var newForms []string
	seen := make(map[string]struct{}, len(baseWords))
	for _, w := range baseWords {
		for _, form := range englishInflections(w) {
			if _, isBase := baseSet[form]; isBase {
				continue
			}
			if _, dup := seen[form]; dup {
				continue
			}
			seen[form] = struct{}{}
			newForms = append(newForms, form)
		}
	}

	// Multi-row batch insert — 250 rows per statement keeps us under
	// SQLite's default 500-variable limit (250 * 2 params = 500).
	if err := bulkInsertWords(db, newForms, "en", 250); err != nil {
		return fmt.Errorf("affix-en: %w", err)
	}
	slog.Info("affix-en loaded", "base_words", len(baseWords), "new_forms", len(newForms))
	return nil
}

// bulkInsertWords inserts words in multi-row batches within a single transaction.
// batchSize controls how many rows per INSERT statement (keep ≤250 for SQLite's
// default 500-variable limit since each row uses 2 params).
func bulkInsertWords(db *sql.DB, words []string, lang string, batchSize int) error {
	if len(words) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("bulk insert: begin tx: %w", err)
	}
	defer tx.Rollback()

	for i := 0; i < len(words); i += batchSize {
		end := i + batchSize
		if end > len(words) {
			end = len(words)
		}
		batch := words[i:end]

		var b strings.Builder
		b.WriteString("INSERT OR IGNORE INTO words (word, lang) VALUES ")
		args := make([]any, 0, len(batch)*2)
		for j, w := range batch {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString("(?,?)")
			args = append(args, w, lang)
		}

		if _, err := tx.Exec(b.String(), args...); err != nil {
			return fmt.Errorf("bulk insert: exec: %w", err)
		}
	}

	return tx.Commit()
}

// englishInflections generates common English inflected forms from a base word.
// Skips words that are too short, too long, or already look like inflected forms.
func englishInflections(word string) []string {
	if len(word) < 3 || len(word) > 15 {
		return nil
	}
	// Skip words that already look inflected — avoids "runnings", "happinesses", etc.
	if strings.HasSuffix(word, "ing") || strings.HasSuffix(word, "ness") ||
		strings.HasSuffix(word, "ment") || strings.HasSuffix(word, "tion") ||
		strings.HasSuffix(word, "sion") || strings.HasSuffix(word, "ally") ||
		strings.HasSuffix(word, "ised") || strings.HasSuffix(word, "ized") {
		return nil
	}

	var forms []string
	add := func(s string) {
		if s != word && s != "" {
			forms = append(forms, s)
		}
	}

	last := word[len(word)-1]

	isVowel := func(b byte) bool {
		return b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u'
	}

	// Plural / verb -s forms
	switch {
	case strings.HasSuffix(word, "s") || strings.HasSuffix(word, "x") ||
		strings.HasSuffix(word, "z") || strings.HasSuffix(word, "ch") ||
		strings.HasSuffix(word, "sh"):
		add(word + "es")
	case strings.HasSuffix(word, "y") && len(word) >= 2 && !isVowel(word[len(word)-2]):
		add(word[:len(word)-1] + "ies")
	default:
		add(word + "s")
	}

	// Past tense / past participle -ed, present participle -ing
	switch {
	case last == 'e':
		add(word + "d")    // bake -> baked
		add(word[:len(word)-1] + "ing") // bake -> baking
	case strings.HasSuffix(word, "y") && len(word) >= 2 && !isVowel(word[len(word)-2]):
		add(word[:len(word)-1] + "ied")  // carry -> carried
		add(word + "ing")                 // carry -> carrying
	case len(word) >= 3 && !isVowel(last) && isVowel(word[len(word)-2]) && !isVowel(word[len(word)-3]) &&
		last != 'w' && last != 'x' && last != 'y':
		// CVC pattern: double consonant (run -> running, stop -> stopped)
		add(word + string(last) + "ed")
		add(word + string(last) + "ing")
		// Also add without doubling (some words don't double)
		add(word + "ed")
		add(word + "ing")
	default:
		add(word + "ed")
		add(word + "ing")
	}

	// Comparative / superlative for short adjectives
	if len(word) <= 7 {
		switch {
		case last == 'e':
			add(word + "r")
			add(word + "st")
		case strings.HasSuffix(word, "y") && len(word) >= 2 && !isVowel(word[len(word)-2]):
			add(word[:len(word)-1] + "ier")
			add(word[:len(word)-1] + "iest")
		default:
			add(word + "er")
			add(word + "est")
		}
	}

	// -ly adverb form
	switch {
	case strings.HasSuffix(word, "le"):
		add(word[:len(word)-2] + "ly")
	case strings.HasSuffix(word, "y") && len(word) >= 2:
		add(word[:len(word)-1] + "ily")
	case strings.HasSuffix(word, "ic"):
		add(word + "ally")
	default:
		add(word + "ly")
	}

	// -ness
	if strings.HasSuffix(word, "y") && len(word) >= 2 && !isVowel(word[len(word)-2]) {
		add(word[:len(word)-1] + "iness")
	} else {
		add(word + "ness")
	}

	return forms
}
