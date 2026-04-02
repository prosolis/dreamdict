# DreamDict — Standalone Dictionary Service: Implementation Blueprint

## Overview

`dreamdict` is a self-contained HTTP dictionary service providing word validation, random
word generation, definitions, synonyms, and cross-language translation for English (`en`),
French (`fr`), and European Portuguese (`pt-PT`).

It runs as a plain Go binary managed by systemd on the same host as GogoBee and Ollama.
GogoBee calls it over localhost HTTP. No Docker, no Traefik, no external network exposure.

If GogoBee later containerizes, only the service URL in GogoBee's config changes. The
service itself is unmodified.

---

## Motivation

GogoBee is approaching 50,000 lines of code across 47+ modules. A dictionary package with
loaders, a SQLite dependency, and an import CLI would compound that bloat. Dictionary logic
has no business living inside a Matrix bot. `dreamdict` is a clean extraction: self-contained
infrastructure with a stable HTTP API, independently deployable, independently updatable,
with no knowledge of Matrix, rooms, or bot commands.

---

## Repository

Separate repository: `dreamdict` (not a GogoBee internal package).

```
dreamdict/
├── cmd/
│   ├── server/
│   │   └── main.go          # HTTP server binary
│   └── dictimport/
│       └── main.go          # One-time data import CLI
├── internal/
│   ├── dictionary/
│   │   ├── dict.go          # Core package: public API
│   │   ├── db.go            # SQLite connection + schema bootstrap
│   │   └── query.go         # IsValidWord, RandomWord, Define, Synonyms, Translate
│   └── loader/
│       ├── loader.go        # Shared interface + orchestrator
│       ├── scowl.go         # English word list
│       ├── hunspell.go      # French + pt-PT word lists (shared, parameterized)
│       ├── wordnet.go       # English definitions + synonyms
│       ├── lexique.go       # French POS + frequency enrichment
│       ├── wolf.go          # French definitions + synonyms
│       ├── dicionario.go    # pt-PT definitions
│       └── wiktionary.go    # All langs: supplemental definitions + translations
├── data/
│   └── .gitkeep             # Source data files (not committed)
├── dict.db                  # Generated SQLite database (not committed)
├── scripts/
│   └── download-dict-data.sh
├── deploy/
│   └── dreamdict.service     # systemd unit file
├── go.mod
└── README.md
```

---

## HTTP API

The server listens on `127.0.0.1:7777` by default (configurable via `--port` flag or
`DREAMDICT_PORT` env var). All responses are JSON. All endpoints are read-only GET requests.

### `GET /valid`

Check if a word exists in a language's word list.

**Query params:** `word` (required), `lang` (required: `en`, `fr`, `pt-PT`)

**Response:**
```json
{"valid": true}
```

---

### `GET /random`

Return a random word for a language, with optional filters.

**Query params:**
- `lang` (required)
- `pos` (optional: `noun`, `verb`, `adjective`, `adverb`)
- `min` (optional: minimum word length, integer)
- `max` (optional: maximum word length, integer)
- `min_freq` (optional: minimum frequency score, integer; only meaningful for `fr`)

**Response:**
```json
{"word": "ephemeral"}
```

**Error (no words match filters):**
```json
{"error": "no_match", "message": "no words match the given filters"}
```

---

### `GET /define`

Return all definitions for a word, ordered by source priority.

**Query params:** `word` (required), `lang` (required)

**Response:**
```json
{
  "word": "ephemeral",
  "lang": "en",
  "definitions": [
    {"pos": "adjective", "gloss": "lasting for a very short time", "source": "wordnet", "priority": 10},
    {"pos": "adjective", "gloss": "lasting only for a day", "source": "wiktionary", "priority": 20}
  ]
}
```

**Word exists but has no definitions:**
```json
{"word": "...", "lang": "...", "definitions": []}
```

---

### `GET /synonyms`

Return deduplicated synonyms for a word.

**Query params:** `word` (required), `lang` (required)

**Response:**
```json
{"word": "happy", "lang": "en", "synonyms": ["glad", "joyful", "content", "pleased"]}
```

---

### `GET /translate`

Return known equivalents of a word in another language.

**Query params:** `word` (required), `from` (required), `to` (required)

**Response:**
```json
{"word": "cat", "from": "en", "to": "fr", "translations": ["chat", "minet"]}
```

---

### `GET /health`

Health check and database stats.

**Response:**
```json
{
  "status": "ok",
  "db_path": "/opt/dreamdict/dict.db",
  "word_counts": {"en": 214832, "fr": 82104, "pt-PT": 71088},
  "def_counts":  {"en": 380000, "fr": 200000, "pt-PT": 120000},
  "imported_at": "2025-03-28T14:22:00Z",
  "schema_version": "1"
}
```

---

## Core Package (`internal/dictionary`)

### `dict.go`

```go
package dictionary

type Options struct {
    MinLength    int
    MaxLength    int
    POS          string  // "", "noun", "verb", "adjective", "adverb"
    MinFrequency int     // 0 = no filter
}

type Definition struct {
    POS      string
    Gloss    string
    Source   string
    Priority int
}

type Dictionary struct { /* unexported */ }

func New(dbPath string) (*Dictionary, error)
func (d *Dictionary) Close() error
func (d *Dictionary) IsValidWord(word, lang string) (bool, error)
func (d *Dictionary) RandomWord(lang string, opts Options) (string, error)
func (d *Dictionary) Define(word, lang string) ([]Definition, error)
func (d *Dictionary) Synonyms(word, lang string) ([]string, error)
func (d *Dictionary) Translate(word, fromLang, toLang string) ([]string, error)
func (d *Dictionary) WordCount() (map[string]int, error)
func (d *Dictionary) SupportedLangs() ([]string, error)

func ValidLang(lang string) bool  // "en", "fr", "pt-PT"

var (
    ErrNoMatch   = errors.New("dictionary: no word matches filters")
    ErrNotSeeded = errors.New("dictionary: database not seeded, run: go run ./cmd/dictimport")
)
```

---

## Database Schema

Use `modernc.org/sqlite` (pure Go, no CGO).

```sql
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
```

### Definition Priority

| Source | Priority |
|---|---|
| `wordnet` | 10 |
| `wolf` | 10 |
| `dicionario-aberto` | 10 |
| `wiktionary` | 20 |

---

## Server Binary (`cmd/server/main.go`)

### Flags / Env Vars

```
--port      PORT     Listen port (default 7777)       DREAMDICT_PORT
--db        PATH     Path to dict.db                  DREAMDICT_DB
--host      HOST     Listen host (default 127.0.0.1)  DREAMDICT_HOST
```

Default host is `127.0.0.1` — the service binds localhost only. It is never exposed to the
external network. If GogoBee containerizes in future, change host to `0.0.0.0` and control
access at the Docker network level.

### Startup Behaviour

- Open the SQLite database on startup; log and exit if unavailable or not seeded
- Register HTTP handlers for all six endpoints
- Log `dreamdict listening on 127.0.0.1:7777` on successful start
- Handle `SIGTERM` / `SIGINT` for graceful shutdown (close DB, drain in-flight requests)

### HTTP Handler Pattern

All handlers follow the same structure:
1. Parse and validate query params — return `400` with `{"error": "...", "message": "..."}` on bad input
2. Call the appropriate `dictionary.*` method
3. Return `200` with JSON response, or `404` if the word is not found, or `500` on
   unexpected errors
4. Log errors at ERROR level; log each request at DEBUG level (controllable via `--log-level`)

Use only standard library `net/http` — no framework needed for six endpoints.

---

## Import CLI (`cmd/dictimport/main.go`)

### Flags

```
--db        Path to output dict.db     (default: ./dict.db)
--data      Path to data directory     (default: ./data/)
--langs     Comma-separated langs      (default: en,fr,pt-PT)
--clean     Drop and recreate all tables before import
--skip      Comma-separated loader names to skip
```

### Execution Order

**English:**
1. `scowl` — populates words
2. `wordnet` — definitions + synonyms
3. `wiktionary-en` — supplemental definitions, synonyms, translations

**French:**
1. `hunspell-fr` — populates words
2. `lexique` — enriches POS + frequency
3. `wolf` — definitions + synonyms
4. `wiktionary-fr` — supplemental definitions, synonyms, translations

**pt-PT:**
1. `hunspell-pt` — populates words
2. `dicionario` — definitions
3. `wiktionary-pt` — supplemental definitions, synonyms, translations

Use `INSERT OR IGNORE` throughout. Wrap each loader in a single transaction; commit on
completion. Print per-loader row counts and elapsed time. Print final summary.

---

## Data Sources & Loader Specs

---

### English — SCOWL (`loader/scowl.go`)

**Source:** https://wordlist.aspell.net/
**Files:** All `final/english-words.NN` where NN ≤ 70

One word per line. Skip blank lines and `#` comments. Lowercase. Skip words with
spaces, digits, hyphens, or apostrophes. `INSERT OR IGNORE INTO words (word, lang) VALUES (?, 'en')`.

**Expected:** ~170,000–220,000 words.

---

### French + pt-PT — Hunspell (`loader/hunspell.go`)

**Source:** https://github.com/LibreOffice/dictionaries
**Files:** `fr_FR/fr_FR.dic`, `pt_PT/pt_PT.dic`

Skip line 1 (word count). Split each line on `/`, take left side as base word. Lowercase.
Skip words with digits, spaces, or non-Latin characters.

Same function handles both languages: `func Load(path string, lang string, db *sql.DB) error`

**Expected:** ~60,000–90,000 words per language.

---

### English Definitions + Synonyms — WordNet (`loader/wordnet.go`)

**Source:** https://wordnet.princeton.edu/download/current-version
**Files:** `dict/data.noun`, `dict/data.verb`, `dict/data.adj`, `dict/data.adv`

Skip lines starting with two spaces (headers). Parse `ss_type` for POS. Extract all words
in synset. Take gloss text before the first `;` (drops usage examples). Insert definitions
at `priority=10`. Insert intra-synset word pairs as synonyms. Skip words containing `_`
for synonym insertion.

---

### French Enrichment — Lexique (`loader/lexique.go`)

**Source:** http://www.lexique.org/?page_id=319
**File:** `Lexique383.tsv`

Columns: 0=`ortho`, 3=`cgram`, 8=`freqfilms2`. POS map: `NOM→noun`, `VER→verb`,
`ADJ→adjective`, `ADV→adverb`. Frequency = `freqfilms2 × 1000` truncated to int.
On conflict with existing Hunspell row: `UPDATE ... SET pos=?, frequency=? WHERE pos IS NULL`.

---

### French Definitions + Synonyms — WOLF (`loader/wolf.go`)

**Source:** https://github.com/nicolashernandez/WOLF
**File:** `wolf-1.0b4.xml.bz2` → decompress to `wolf.xml`

Stream-parse XML with `encoding/xml`. For each `<SYNSET>`: collect `<LITERAL>` values,
map `<POS>`, insert definitions at `priority=10`, insert intra-synset synonym pairs.

---

### pt-PT Definitions — Dicionário Aberto (`loader/dicionario.go`)

**Source:** https://github.com/jorgecardoso/dicionario-aberto
**File:** `dicionario-aberto.xml`

**⚠️ Inspect the file before writing the parser:**
```bash
head -100 data/dicionario-aberto.xml
```
Print distinct `<pos>` values before writing the POS map. Element names in the live file
may differ from older documentation. Stream-parse XML. Insert definitions at `priority=10`.

---

### All Languages — Wiktionary (`loader/wiktionary.go`)

**Source:** kaikki.org pre-parsed Wiktionary JSONL dumps

| Target file | Lang | URL |
|---|---|---|
| `kaikki-en.jsonl` | `en` | https://kaikki.org/dictionary/English/kaikki.org-dictionary-English.json |
| `kaikki-fr.jsonl` | `fr` | https://kaikki.org/dictionary/French/kaikki.org-dictionary-French.json |
| `kaikki-pt.jsonl` | `pt-PT` | https://kaikki.org/dictionary/Portuguese/kaikki.org-dictionary-Portuguese.json |

**JSON structure per line:**
```json
{
  "word": "example",
  "pos": "noun",
  "lang_code": "en",
  "senses": [
    {"glosses": ["a representative instance"], "tags": []},
    {"glosses": ["past tense of exemplify"], "tags": ["form-of"]}
  ],
  "synonyms": [{"word": "instance"}],
  "translations": [
    {"code": "fr", "word": "exemple"},
    {"code": "pt", "word": "exemplo"}
  ]
}
```

**POS mapping:** `noun→noun`, `verb→verb`, `adj→adjective`, `adv→adverb`, `name→noun`.

**Import logic:**
- Use `bufio.Scanner` with 10MB buffer (default 64KB truncates long lines silently)
- Wrap in single transaction; commit every 10,000 rows
- Verify `lang_code` matches expected language; skip if not
- Lowercase word; skip if contains spaces or digits
- Skip senses tagged `form-of` or `alt-of`
- Skip glosses that are empty, start with `(`, or exceed 500 characters
- Insert definitions at `priority=20`; `UNIQUE(word_id, gloss)` handles deduplication silently
- Insert synonyms; `UNIQUE(word_id, synonym)` handles deduplication silently
- **Translations:** for each entry in `translations[]`, if `code` maps to a supported lang
  and `code != source lang`, insert into `translations` table
  - Code mapping: `fr→fr`, `pt→pt-PT`, `en→en`
  - Skip translations where `word` contains spaces or digits

**pt-PT Brazil filtering:** Reject senses where `tags` contains `"Brazil"` or
`"Brazilian Portuguese"`. Accept untagged senses and senses tagged `"Portugal"` or
`"European Portuguese"`.

**Expected additional definitions after deduplication:**
- en: ~200,000–400,000
- fr: ~80,000–150,000
- pt-PT: ~40,000–80,000

---

## Deployment (systemd)

### File Locations

```
/usr/local/bin/dreamdict          # compiled binary
/opt/dreamdict/dict.db            # SQLite database
/opt/dreamdict/data/              # source data files (can be deleted after import)
/etc/dreamdict/dreamdict.env       # environment config
/var/log/dreamdict/               # logs (or use journald)
```

### `deploy/dreamdict.service`

```ini
[Unit]
Description=DreamDict dictionary service
After=network.target
# If GogoBee ever runs as a service too:
# Before=dreamdict.service

[Service]
Type=simple
User=dreamdict
Group=dreamdict
EnvironmentFile=/etc/dreamdict/dreamdict.env
ExecStart=/usr/local/bin/dreamdict server \
    --host 127.0.0.1 \
    --port 7777 \
    --db /opt/dreamdict/dict.db
Restart=on-failure
RestartSec=5s

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/dreamdict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### `/etc/dreamdict/dreamdict.env`

```
DREAMDICT_PORT=7777
DREAMDICT_DB=/opt/dreamdict/dict.db
```

### Install Steps

```bash
# Create user
useradd --system --no-create-home --shell /usr/sbin/nologin dreamdict

# Create dirs
mkdir -p /opt/dreamdict /etc/dreamdict /var/log/dreamdict
chown dreamdict:dreamdict /opt/dreamdict /var/log/dreamdict

# Build and install binary
go build -o /usr/local/bin/dreamdict ./cmd/server
chmod 755 /usr/local/bin/dreamdict

# Run import (as yourself, not dreamdict user)
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --data /opt/dreamdict/data/
chown dreamdict:dreamdict /opt/dreamdict/dict.db

# Install and start service
cp deploy/dreamdict.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now dreamdict

# Verify
systemctl status dreamdict
curl http://127.0.0.1:7777/health
```

### Updating the Database

```bash
# Stop service, rebuild database, restart
systemctl stop dreamdict
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --clean
chown dreamdict:dreamdict /opt/dreamdict/dict.db
systemctl start dreamdict
```

---

## GogoBee Integration

GogoBee gets a single `DreamDictClient` struct. Zero dictionary logic inside the bot.

### Client Package

Add to GogoBee: `internal/dreamclient/client.go`

```go
package dreamclient

import (
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "time"
)

type Client struct {
    baseURL    string
    httpClient *http.Client
}

func New(baseURL string) *Client {
    return &Client{
        baseURL: baseURL,
        httpClient: &http.Client{Timeout: 3 * time.Second},
    }
}

func (c *Client) IsValidWord(word, lang string) (bool, error)
func (c *Client) RandomWord(lang string, pos string, min, max int) (string, error)
func (c *Client) Define(word, lang string) ([]Definition, error)
func (c *Client) Synonyms(word, lang string) ([]string, error)
func (c *Client) Translate(word, fromLang, toLang string) ([]string, error)
func (c *Client) Health() (*HealthResponse, error)
```

### Configuration

In GogoBee's config:
```yaml
dictionary:
  url: "http://127.0.0.1:7777"
```

When GogoBee eventually containerizes, this becomes the container network address.
No other changes required.

### Replacing Wordnik Calls

Run `grep -r -i wordnik .` in the GogoBee repo to find all call sites. Replace:

| Wordnik call | GogoBee replacement |
|---|---|
| `GET /word/{word}/valid` | `dictClient.IsValidWord(word, lang)` |
| `GET /words/randomWord` | `dictClient.RandomWord(lang, pos, min, max)` |
| `GET /word/{word}/definitions` | `dictClient.Define(word, lang)` |
| `GET /word/{word}/relatedWords` | `dictClient.Synonyms(word, lang)` |

Calls previously hardcoded to English: keep `lang = "en"`.

### Startup Behaviour

On GogoBee startup, call `/health` to verify dreamdict is reachable. Log a warning (not
a fatal error) if unavailable — dictionary-dependent commands should fail gracefully with
a user-facing message rather than taking the whole bot down.

---

## Data File Checklist

| File | Lang | Source |
|---|---|---|
| `scowl/final/english-words.*` | en | wordlist.aspell.net |
| `wordnet/dict/data.{noun,verb,adj,adv}` | en | wordnet.princeton.edu |
| `kaikki-en.jsonl` | en | kaikki.org |
| `fr_FR.dic` | fr | github.com/LibreOffice/dictionaries |
| `Lexique383.tsv` | fr | lexique.org |
| `wolf.xml` | fr | github.com/nicolashernandez/WOLF |
| `kaikki-fr.jsonl` | fr | kaikki.org |
| `pt_PT.dic` | pt-PT | github.com/LibreOffice/dictionaries |
| `dicionario-aberto.xml` | pt-PT | github.com/jorgecardoso/dicionario-aberto |
| `kaikki-pt.jsonl` | pt-PT | kaikki.org |

`scripts/download-dict-data.sh` automates all downloads. Idempotent — skips existing files
unless `--force` passed. Handles tar extraction (SCOWL), bz2 decompression (WOLF), and
git clones where no stable direct URL exists.

---

## `go.mod` Dependencies

```
modernc.org/sqlite    # Pure-Go SQLite, no CGO
```

Standard library covers everything else: `net/http`, `encoding/xml`, `encoding/json`,
`bufio`, `compress/bzip2`, `database/sql`, `flag`, `log/slog`.

---

## Testing

### Service Tests (`internal/dictionary/dict_test.go`)

Use `:memory:` SQLite. Cover:

1. `IsValidWord` — known word true, unknown false, case-insensitive
2. `RandomWord` — matches length + POS filters; returns `ErrNoMatch` on empty set
3. `Define` — priority ordering (wordnet before wiktionary); empty slice for no definitions
4. `Synonyms` — deduplicated; empty slice when none
5. `Translate` — returns correct target-lang translations; empty slice when none
6. `ErrNotSeeded` — when `meta` table has no rows
7. Gloss deduplication — identical gloss twice → one row
8. Synonym deduplication — identical synonym twice → one row
9. Translation deduplication — identical translation twice → one row
10. `form-of` filtering — senses tagged `form-of` not inserted
11. pt-PT Brazil filtering — `Brazil`-tagged senses rejected; untagged accepted

### HTTP Handler Tests (`cmd/server/`)

Use `httptest.NewServer`. Cover:

1. `/valid` — 200 true/false, 400 on missing params
2. `/random` — 200 with word, 404 on no match
3. `/define` — 200 with ordered definitions, 200 with empty array on no defs
4. `/synonyms` — 200 with list, 200 with empty array
5. `/translate` — 200 with list, 200 with empty array
6. `/health` — 200 with counts

---

## Expected Database Size

| Language | Words | Definitions | Approx size |
|---|---|---|---|
| en | ~220,000 | ~380,000 | ~180MB |
| fr | ~90,000 | ~200,000 | ~80MB |
| pt-PT | ~75,000 | ~120,000 | ~50MB |
| **Total** | | | **~310MB** |

Add to `.gitignore`: `dict.db`, `data/`

---

## Phase 2: Affix Expansion

**Implement shortly after initial launch, not at initial launch.**

The base-forms-only approach in the initial import means `IsValidWord("running", "en")`
returns false because only `"run"` is stored. For Hangman this doesn't matter — DreamDict
generates the puzzle word and it's always a base form. But for any command where a player
submits a word for validation (UNO, word games, future puzzle modes), this is a real
correctness bug, not a theoretical one.

**Recommended approach:** affix expansion at import time using `go-hunspell`
(`github.com/trustmaster/go-hunspell`). Parse the `.aff` rules for each language and
generate all valid inflected forms during `dictimport`, storing them in the `words` table
alongside base forms. This keeps `IsValidWord` as a simple indexed lookup with no runtime
stemming logic.

**Do not implement stemming as an alternative.** Stemming at query time is more complex,
language-specific, and redundant if you expand affixes at import time. Pick one direction —
affix expansion at import is the cleaner path for this architecture.

**Sequencing:** get the service running and validate data quality across all three languages
first. Affix expansion is a `dictimport` change only — the HTTP API, schema, and GogoBee
client are untouched.

---

## Deferred: Traefik Route for Second Consumer

When a second consumer for DreamDict exists, adding a Traefik route on parodia.dev's
internal network is trivial — one label, one DNS entry, twenty minutes of work. Do it
then, not now. The service architecture requires no changes at that point.

---

## Not Worth Doing: HTTP Caching Headers

The consumers are on localhost, there is no intermediary cache, and SQLite responses are
already faster than any cache lookup. Drop this entirely.

---

## Phase 2

Implement after initial launch is stable and data quality has been validated across all
three languages. Each item below is scoped and sequenced independently -- they do not
depend on each other unless noted.

---

### P2-1: Hunspell Affix Expansion

See "Phase 2: Affix Expansion" section above. Implement first among Phase 2 items as it
fixes a real correctness bug for player-submitted word validation.

---

### P2-2: WordNet Synset Cross-Language Backing

**The problem with the current translation approach:**
The current `translations` table is populated opportunistically from Wiktionary tags, which
are inconsistently applied. Coverage is good for common words and thin everywhere else.
This is a structural ceiling, not a data quality problem that more sources can fix.

**The intentional approach:**
Princeton WordNet assigns every concept a stable synset ID. WOLF maps French words to
those same synset IDs. The Open Multilingual Wordnet (OMW) does the same for Portuguese.
Two words in different languages that share a synset ID are semantic equivalents with high
confidence -- two independent projects made that mapping independently. DreamDict currently
discards synset IDs during import. Preserving them unlocks proper cross-language backing.

**Schema additions:**

```sql
CREATE TABLE IF NOT EXISTS synsets (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    synset_id TEXT NOT NULL UNIQUE,  -- Princeton WN ID e.g. '00914031-a'
    pos       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS word_synsets (
    word_id   INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    synset_id INTEGER NOT NULL REFERENCES synsets(id) ON DELETE CASCADE,
    source    TEXT NOT NULL,         -- 'wordnet', 'wolf', 'omw'
    PRIMARY KEY (word_id, synset_id)
);

CREATE INDEX IF NOT EXISTS idx_word_synsets_word_id   ON word_synsets(word_id);
CREATE INDEX IF NOT EXISTS idx_word_synsets_synset_id ON word_synsets(synset_id);
```

**Loader changes:**
- `wordnet.go` -- store the synset offset as `synset_id` in `synsets`, link words via
  `word_synsets` with `source='wordnet'`
- `wolf.go` -- WOLF synset IDs are Princeton WN IDs; store and link with `source='wolf'`
- New loader `omw.go` -- Open Multilingual Wordnet Portuguese data; maps pt words to
  Princeton synset IDs; link with `source='omw'`
  - Source: https://github.com/omwn/omw-data
  - File: Portuguese tab file from the OMW release

**New API method:**

```go
// EnglishBacking returns English words and their WordNet definitions that share
// a synset with the given word. Semantic equivalence, not translation guesswork.
func (d *Dictionary) EnglishBacking(word, lang string) ([]EnglishEquivalent, error)

type EnglishEquivalent struct {
    Word       string
    Definition string  // the WordNet gloss for that synset
    Synset     string
}
```

**New endpoint:**

```
GET /backing?word=éphémère&lang=fr
→ {
    "word": "éphémère",
    "lang": "fr",
    "equivalents": [
      {"word": "ephemeral", "definition": "lasting a very short time", "synset": "00914031-a"}
    ]
  }
```

**Fallback behaviour:**
Words with no synset mapping (proper nouns, invented words, very obscure entries) fall back
to the existing `translations` table as a secondary source. The two mechanisms complement
each other -- synsets for authoritative coverage, Wiktionary translations for the rest.

**GogoBee impact:**
Every French and Portuguese word with a WOLF or OMW synset mapping automatically gets
authoritative English backing with a real WordNet definition attached -- not just a word,
but the concept it represents. Update `DreamDictClient` with a `EnglishBacking()` method.

---

### P2-3: Antonyms

**Data source:** Already in Princeton WordNet. The pointer data in `data.*` files includes
antonym relations marked with `!` as the pointer symbol. The current `wordnet.go` loader
skips all pointer types. This is a loader change only.

**Schema addition:**

```sql
CREATE TABLE IF NOT EXISTS antonyms (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    antonym  TEXT    NOT NULL,
    source   TEXT    NOT NULL,   -- 'wordnet', 'wiktionary'
    UNIQUE(word_id, antonym)
);

CREATE INDEX IF NOT EXISTS idx_antonyms_word_id ON antonyms(word_id);
```

**Loader changes:**
- `wordnet.go` -- parse pointer lines with symbol `!`; for each antonym pointer, insert
  into `antonyms` with `source='wordnet'`
- `wiktionary.go` -- kaikki.org entries carry an `antonyms[]` array alongside `synonyms[]`;
  parse and insert with `source='wiktionary'`

**New API method and endpoint:**

```go
func (d *Dictionary) Antonyms(word, lang string) ([]string, error)
```

```
GET /antonyms?word=happy&lang=en
→ {"word": "happy", "lang": "en", "antonyms": ["unhappy", "sad", "miserable"]}
```

**GogoBee uses:** Opposite-word puzzle modes, richer `!define` output, future word game
mechanics that need semantic opposites.

---

### P2-4: Frequency Weighting for English and Portuguese

**The problem:** `RandomWord()` for English and Portuguese is currently unweighted --
`ORDER BY RANDOM()` across the full word table. A Hangman puzzle is as likely to return
"aardvark" as "house." French already has frequency data from Lexique. English and
Portuguese need the same.

**Data sources:**

- **English:** SUBTLEX-US (Brysbaert & New, 2009) -- subtitle frequency corpus, freely
  available. Provides word frequency per million from film/TV subtitles. Same format as
  Lexique conceptually.
  - URL: http://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus
  - File: `SUBTLEX-US.xlsx` (export to TSV before import)

- **Portuguese:** SUBTLEX-PT exists but coverage skews pt-BR. Better option for pt-PT is
  the frequency list derived from the CETEMPúblico corpus (European Portuguese newspaper
  corpus), available via Linguateca.
  - URL: https://www.linguateca.pt/acesso/corpus.php?corpus=CETEMPUBLICO
  - Alternative: use Wiktionary frequency tags as a rough proxy if CETEMPúblico access
    is cumbersome -- lower quality but available immediately.

**Loader changes:**
- New `subtlex.go` -- TSV import for English frequency, same pattern as `lexique.go`;
  `UPDATE words SET frequency=? WHERE word=? AND lang='en' AND frequency=0`
- New `cetempublico.go` (or `wiktfreq.go` as fallback) -- same pattern for pt-PT

**Schema:** No change -- `frequency` column already exists on `words` for all languages.

**API change:** `MinFrequency` in `Options` already exists. No API change needed -- this
just makes the filter meaningful for `en` and `pt-PT` for the first time.

**DreamDict dictimport change:** Add both loaders to the English and pt-PT execution order,
running after word list population.

---

## Phase 3

Implement after Phase 2 is complete and stable. These are higher-effort or lower-urgency
than Phase 2 items but all have clear value.

---

### P3-1: Word Difficulty Scoring

**What it is:** A composite difficulty score derived from data already in the database --
no new sources required. Computed at import time and stored as a column.

**Formula (tune after testing):**
```
difficulty = normalize(1 / frequency) * 0.5
           + normalize(length) * 0.3
           + normalize(syllable_count) * 0.2
```

Syllable count can be approximated from the word string (vowel cluster counting) or pulled
from CMU Pronouncing Dictionary (see P3-2) once that data exists.

**Schema addition:**

```sql
ALTER TABLE words ADD COLUMN difficulty REAL DEFAULT NULL;
-- NULL = not yet scored; 0.0 = easiest; 1.0 = hardest
```

**API change:**

Add to `Options`:
```go
MaxDifficulty float64  // 0.0–1.0; 0 = no filter
MinDifficulty float64
```

Add to `RandomWord()` response (exposed via new `WordDetail` return type if needed) and
as a metadata field on `/random` response:
```json
{"word": "ephemeral", "difficulty": 0.74}
```

**GogoBee uses:** Tiered Hangman difficulty without hardcoded word lists. "Easy", "Medium",
"Hard" modes map to difficulty ranges. Arena word puzzles can scale to player level.

---

### P3-2: Pronunciation Data

**Data source:** CMU Pronouncing Dictionary (CMUdict)
- URL: http://www.speech.cs.cmu.edu/cgi-bin/cmudict
- Format: plain text, one entry per line: `EPHEMERAL  IH0 F EH1 M ER0 AH0 L`
- Coverage: English only (~134,000 entries)
- License: BSD-style, freely usable

For French and Portuguese, IPA data is available in the kaikki.org Wiktionary dumps under
a `sounds` array per entry -- the current wiktionary loader ignores this field.

**Schema addition:**

```sql
CREATE TABLE IF NOT EXISTS pronunciations (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id  INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    format   TEXT NOT NULL,   -- 'cmu', 'ipa'
    value    TEXT NOT NULL,
    source   TEXT NOT NULL,   -- 'cmudict', 'wiktionary'
    UNIQUE(word_id, format, source)
);

CREATE INDEX IF NOT EXISTS idx_pronunciations_word_id ON pronunciations(word_id);
```

**Loader changes:**
- New `cmudict.go` -- plain text import; match words to existing `words` table entries;
  insert with `format='cmu'`, `source='cmudict'`
- `wiktionary.go` -- extend to parse `sounds[].ipa` field; insert with `format='ipa'`,
  `source='wiktionary'`

**New API method and endpoint:**

```go
func (d *Dictionary) Pronunciation(word, lang string) ([]Pronunciation, error)

type Pronunciation struct {
    Format string  // 'cmu', 'ipa'
    Value  string
    Source string
}
```

```
GET /pronunciation?word=ephemeral&lang=en
→ {
    "word": "ephemeral",
    "lang": "en",
    "pronunciations": [
      {"format": "cmu", "value": "IH0 F EH1 M ER0 AH0 L", "source": "cmudict"},
      {"format": "ipa", "value": "ɪˈfɛm.ər.əl", "source": "wiktionary"}
    ]
  }
```

**GogoBee uses:** `!define` output enrichment, syllable count for difficulty scoring (P3-1),
future rhyme game mechanics (words sharing a CMU tail sequence are rhymes).

---

### P3-3: Etymology

**Data source:** kaikki.org Wiktionary dumps already downloaded -- etymology data is in the
`etymology_text` field per entry. The current `wiktionary.go` loader ignores it.

**Schema addition:**

```sql
CREATE TABLE IF NOT EXISTS etymology (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    word_id    INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,   -- free-form etymology string from Wiktionary
    source     TEXT NOT NULL,   -- 'wiktionary'
    UNIQUE(word_id, source)
);

CREATE INDEX IF NOT EXISTS idx_etymology_word_id ON etymology(word_id);
```

**Loader change:**
- `wiktionary.go` -- extend to parse `etymology_text` field; insert into `etymology` if
  non-empty and not a redirect/stub string (skip entries that are just "See X" or empty
  after trimming)

**New API method and endpoint:**

```go
func (d *Dictionary) Etymology(word, lang string) (string, error)
```

```
GET /etymology?word=ephemeral&lang=en
→ {
    "word": "ephemeral",
    "lang": "en",
    "etymology": "From Medieval Latin ephemerus, from Ancient Greek ἐφήμερος (ephḗmeros, \"lasting only a day\"), from ἐπί (epí, \"on\") + ἡμέρα (hēméra, \"day\")."
  }
```

**GogoBee uses:** `!etymology` command for the community. Particularly relevant given the
pt-PT context -- shared Latin/Greek roots between Portuguese, French, and English make
etymology a genuine learning tool, not just trivia. Word of the Day can optionally append
a one-line etymology note.

**Quality note:** Wiktionary etymology text is free-form and inconsistently structured.
Surface it as-is rather than attempting to parse it into structured fields. A raw string
is useful; a badly parsed structured field is not.

---

## Roadmap Summary

| Phase | Item | Effort | Payoff |
|---|---|---|---|
| 2 | Affix expansion | Medium | High -- fixes real validation bug |
| 2 | Synset cross-language backing | Medium | High -- authoritative multilingual semantics |
| 2 | Antonyms | Low | Medium -- already in WordNet data |
| 2 | Frequency for en + pt-PT | Low-Medium | High -- better random word quality |
| 3 | Difficulty scoring | Low | High -- derived, no new data |
| 3 | Pronunciation | Medium | Medium -- en only initially |
| 3 | Etymology | Low | High -- data already downloaded |
