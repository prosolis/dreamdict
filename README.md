# DreamDict

A self-hosted, CGO-free HTTP dictionary service supporting English, French, **European Portuguese**, Spanish, and Mandarin Chinese. Built to replace metered third-party APIs like Wordnik with something you own outright.

No API keys. No rate limits. No external runtime dependencies. Sub-millisecond response times on commodity hardware.

---

## Why DreamDict exists

Most open source dictionary tooling is either a dataset (WordNet, Wiktionary dumps, Hunspell) or a desktop application. There is no self-hosted HTTP service that combines all of them into a programmable API — so we built one.

The pt-PT distinction matters and is handled explicitly. Most open NLP resources default to Brazilian Portuguese (pt-BR) or treat both variants as interchangeable. DreamDict uses the LibreOffice pt-PT Hunspell dictionary, Dicionário Aberto, CETEMPúblico frequency data, and Wiktionary entries filtered to European Portuguese. If you are building something for a European Portuguese-speaking audience, this is the only self-hosted option that takes that seriously.

**Benchmark results (end-to-end, including HTTP and JSON serialization):**

| Endpoint | Latency | Throughput |
|---|---|---|
| GET /valid | ~20 µs | ~50,000 req/s |
| GET /random (filtered) | ~187 µs | ~5,300 req/s |
| GET /define | ~50 µs | ~20,000 req/s |
| GET /synonyms | ~37 µs | ~27,000 req/s |
| GET /translate | ~56 µs | ~18,000 req/s |

---

## Requirements

- Go 1.25+
- No CGO required — uses [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), a pure Go SQLite port
- Single static binary, no runtime dependencies

---

## Quick Start

```bash
# Download all dictionary source data (~3-4 GB)
./scripts/download-dict-data.sh

# Build the database (~400 MB, one-time)
go run ./cmd/dictimport --data ./data --db ./dict.db

# Start the server
go run ./cmd/server --db ./dict.db
```

The server listens on `127.0.0.1:7777` by default.

---

## API

All endpoints are read-only `GET` requests returning JSON. When authentication is enabled, all endpoints except `/health` require a bearer token (see [Authentication](#authentication)).

### `GET /valid`

Check if a word exists in a language's word list. Includes inflected forms via affix expansion — `running` and `ran` resolve correctly, not just base forms.

```bash
curl 'localhost:7777/valid?word=running&lang=en'
{"valid":true}
```

### `GET /random`

Return a random word. Optional filters: `pos` (`noun`, `verb`, `adjective`, `adverb`), `min`/`max` (word length), `min_freq` (frequency score), `min_difficulty`/`max_difficulty` (0.0–1.0), `variant` (`us` or `gb`, English only).

```bash
curl 'localhost:7777/random?lang=en&pos=noun&min=5&max=8&max_difficulty=0.5'
{"word":"lantern","difficulty":0.42}
```

### `GET /define`

Return all definitions ordered by source priority. Curated sources (WordNet, WOLF, Dicionário Aberto) appear before Wiktionary supplemental definitions. For English words, the response includes a `variant` field (`"us"`, `"gb"`, or omitted for common English) indicating regional spelling.

```bash
curl 'localhost:7777/define?word=ardor&lang=en'
{
  "word": "ardor",
  "lang": "en",
  "variant": "us",
  "definitions": [
    {"pos":"noun","gloss":"a feeling of strong eagerness","source":"wordnet","priority":10},
    {"pos":"noun","gloss":"feelings of great warmth and intensity","source":"wordnet","priority":10}
  ]
}
```

### `GET /synonyms`

Return deduplicated synonyms sourced from WordNet, WOLF, and Wiktionary.

```bash
curl 'localhost:7777/synonyms?word=happy&lang=en'
{"word":"happy","lang":"en","synonyms":["content","glad","joyful","pleased"]}
```

### `GET /antonyms`

Return antonyms sourced from WordNet and Wiktionary.

```bash
curl 'localhost:7777/antonyms?word=happy&lang=en'
{"word":"happy","lang":"en","antonyms":["sad","unhappy"]}
```

### `GET /translate`

Return known translations between supported languages.

```bash
curl 'localhost:7777/translate?word=cat&from=en&to=fr'
{"word":"cat","from":"en","to":"fr","translations":["chat","minet"]}
```

### `GET /backing`

Return English semantic equivalents for a non-English word via shared Princeton WordNet synset IDs. This is semantic equivalence — not translation guesswork. Falls back to the translations table when no synset mapping exists.

```bash
curl 'localhost:7777/backing?word=éphémère&lang=fr'
{
  "word": "éphémère",
  "lang": "fr",
  "equivalents": [
    {"word":"ephemeral","definition":"lasting a very short time","synset":"00914031-a"}
  ]
}
```

### `GET /pronunciation`

Return pronunciation data. English gets CMU phonemes and IPA. All other languages get IPA from Wiktionary where available.

```bash
curl 'localhost:7777/pronunciation?word=ephemeral&lang=en'
{
  "word": "ephemeral",
  "lang": "en",
  "pronunciations": [
    {"format":"cmu","value":"IH0 F EH1 M ER0 AH0 L","source":"cmudict"},
    {"format":"ipa","value":"ɪˈfɛm.ər.əl","source":"wiktionary"}
  ]
}
```

### `GET /etymology`

Return the etymology of a word from Wiktionary. Returned as free-form text — Wiktionary etymology is not structured consistently enough to parse reliably.

```bash
curl 'localhost:7777/etymology?word=ephemeral&lang=en'
{
  "word": "ephemeral",
  "lang": "en",
  "etymology": "From Medieval Latin ephemerus, from Ancient Greek ἐφήμερος (ephḗmeros, \"lasting only a day\")..."
}
```

### `GET /frequency`

Return the frequency score for a word. Higher values = more common. Sourced from SUBTLEX-US (en), Lexique (fr), CETEMPúblico (pt-PT), and OpenSubtitles frequency lists (es).

```bash
curl 'localhost:7777/frequency?word=house&lang=en'
{"word":"house","lang":"en","frequency":800}
```

### `GET /frequency/batch`

Batch frequency lookup. Comma-separated words, max 100 per request.

```bash
curl 'localhost:7777/frequency/batch?words=house,cat,ephemeral&lang=en'
{"lang":"en","frequencies":{"cat":800,"ephemeral":50,"house":800}}
```

### `GET /difficulty`

Return the difficulty score for a word (0.0 = easiest, 1.0 = hardest).

```bash
curl 'localhost:7777/difficulty?word=ephemeral&lang=en'
{"word":"ephemeral","lang":"en","difficulty":0.72}
```

### `GET /words`

Return a list of words matching filters. Same filter options as `/random` plus `variant`. Returns up to 20,000 results.

```bash
curl 'localhost:7777/words?lang=en&min=5&max=5&variant=gb&min_freq=500'
{"lang":"en","count":42,"words":["colour","fibre","honour",...]}
```

### `GET /rhyme`

Return English rhyming words based on CMU phoneme tail matching. Optional `limit` parameter (default 10).

```bash
curl 'localhost:7777/rhyme?word=cat&limit=5'
{"word":"cat","rhymes":["bat","flat","hat","mat","sat"]}
```

### `GET /health`

Health check with database statistics. Always unauthenticated — safe for monitoring tools.

```bash
curl localhost:7777/health
{
  "status": "ok",
  "db_path": "./dict.db",
  "word_counts": {"en":136615,"fr":56096,"pt-PT":136300,"es":118400,"zh":120883},
  "def_counts":  {"en":361640,"fr":137738,"pt-PT":84577,"es":151200,"zh":199929},
  "imported_at": "2026-04-04T21:31:17Z",
  "schema_version": "2"
}
```

### Error Responses

```json
{"error": "bad_request",   "message": "word and lang are required"}
{"error": "no_match",      "message": "no words match the given filters"}
{"error": "unauthorized",  "message": "invalid or missing bearer token"}
```

---

## Configuration

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `--port` | `DREAMDICT_PORT` | `7777` | Listen port |
| `--host` | `DREAMDICT_HOST` | `127.0.0.1` | Listen host (localhost only by default) |
| `--db` | `DREAMDICT_DB` | `./dict.db` | Path to SQLite database |
| `--token` | `DREAMDICT_TOKEN` | _(none)_ | Bearer token for API authentication |
| `--log-level` | | `info` | `debug`, `info`, `warn`, `error` |

Flags take precedence over environment variables.

### Authentication

When `--token` or `DREAMDICT_TOKEN` is set, all endpoints except `/health` require a bearer token. When no token is configured, authentication is disabled.

```bash
# Start with auth enabled
DREAMDICT_TOKEN=your-secret-token go run ./cmd/server --db ./dict.db

# Authenticated request
curl -H 'Authorization: Bearer your-secret-token' 'localhost:7777/define?word=saudade&lang=pt-PT'
```

Requests without a valid token receive a `401` response. The error message does not distinguish between a missing token and an incorrect one.

---

## Import CLI

```bash
go run ./cmd/dictimport [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--db` | `./dict.db` | Output database path |
| `--data` | `./data` | Source data directory |
| `--langs` | `en,fr,pt-PT,es,zh` | Languages to import |
| `--clean` | `false` | Drop and recreate all tables before import |
| `--skip` | | Comma-separated loader names to skip |

### Loaders

| Loader | Language | What it provides |
|---|---|---|
| `scowl` | en | Word list with frequency tiers and regional variant tagging (us/gb) |
| `affix-en` | en | Inflected form expansion |
| `wordnet` | en | Definitions, synonyms, antonyms, synset IDs |
| `subtlex` | en | SUBTLEX-US subtitle frequency data |
| `cmudict` | en | CMU phoneme pronunciation data |
| `wiktionary-en` | en | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `hunspell-fr` | fr | Word list |
| `affix-fr` | fr | Inflected form expansion |
| `lexique` | fr | POS and frequency enrichment |
| `wolf` | fr | Definitions, synonyms, synset IDs |
| `wiktionary-fr` | fr | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `hunspell-pt` | pt-PT | European Portuguese word list (not pt-BR) |
| `affix-pt-PT` | pt-PT | Inflected form expansion |
| `dicionario` | pt-PT | Definitions from Dicionário Aberto |
| `cetempublico` | pt-PT | Frequency data from CETEMPúblico corpus |
| `omw-pt` | pt-PT | Open Multilingual Wordnet synset mappings |
| `wiktionary-pt` | pt-PT | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology (filtered to European Portuguese entries) |
| `hunspell-es` | es | Castilian Spanish word list (es_ES) |
| `affix-es` | es | Inflected form expansion |
| `es-freq` | es | Frequency data from an OpenSubtitles-derived list |
| `omw-es` | es | Open Multilingual Wordnet (MCR) synset mappings |
| `wiktionary-es` | es | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `cedict` | zh | Words, definitions, translations via CC-CEDICT |
| `wiktionary-zh` | zh | Supplemental Chinese data |
| `prune-orphans` | all | Removes ghost words with no definitions, frequency, or references |
| `difficulty` | all | Composite difficulty scoring — runs last |

---

## Data Sources

| Source | Language | What it provides |
|---|---|---|
| [SCOWL](https://wordlist.aspell.net/) | en | Word list + frequency tiers |
| [WordNet 3.0](https://wordnet.princeton.edu/) | en | Definitions, synonyms, antonyms, synset IDs |
| [SUBTLEX-US](http://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus) | en | Subtitle-based word frequency |
| [CMU Pronouncing Dictionary](http://www.speech.cs.cmu.edu/cgi-bin/cmudict) | en | Phoneme pronunciation |
| [Hunspell/LibreOffice](https://github.com/LibreOffice/dictionaries) | fr, pt-PT, es | Word lists + affix rules |
| [Lexique 3.83](http://www.lexique.org/) | fr | POS + frequency enrichment |
| [WOLF](https://github.com/nicolashernandez/WOLF) | fr | Definitions, synonyms, synset IDs |
| [Dicionário Aberto](https://github.com/jorgecardoso/dicionario-aberto) | pt-PT | Definitions |
| [CETEMPúblico](https://www.linguateca.pt/acesso/corpus.php?corpus=CETEMPUBLICO) | pt-PT | European Portuguese word frequency |
| [Open Multilingual Wordnet](https://github.com/omwn/omw-data) | pt-PT, es | Synset mappings to Princeton WordNet |
| [FrequencyWords](https://github.com/hermitdave/FrequencyWords) | es | OpenSubtitles-derived word frequency |
| [CC-CEDICT](https://cc-cedict.org/) | zh | Words, definitions, translations |
| [Wiktionary via kaikki.org](https://kaikki.org/) | all | Definitions, synonyms, antonyms, translations, IPA, etymology |

Run `./scripts/download-dict-data.sh` to fetch all sources automatically. Pass `--force` to re-download existing files.

---

## Difficulty Scoring

Each word receives a composite difficulty score (0.0 = easiest, 1.0 = hardest) computed at import time from frequency, word length, and syllable count:

```
difficulty = inverse_frequency * 0.5 + word_length * 0.3 + syllable_count * 0.2
```

Use `min_difficulty` / `max_difficulty` on `/random` to select words by difficulty tier without maintaining hardcoded word lists.

---

## Database Schema

Schema version 2. Full schema in `internal/dictionary/db.go`.

| Table | Purpose |
|---|---|
| `words` | Core word inventory: word, lang, pos, frequency, difficulty, variant |
| `definitions` | Definitions with source and priority |
| `synonyms` | Synonym relationships |
| `antonyms` | Antonym relationships |
| `translations` | Cross-language translations |
| `synsets` | Princeton WordNet synset IDs |
| `word_synsets` | Word-to-synset mappings for cross-language semantic backing |
| `pronunciations` | CMU phonemes and IPA |
| `etymology` | Word origin text from Wiktionary |
| `meta` | Import metadata: schema version, import timestamp, word/definition counts |

---

## Deployment

A systemd unit file is provided at `deploy/dreamdict.service`. The service runs as a dedicated unprivileged system user and binds to localhost only by default.

```bash
# Create dedicated system user
useradd --system --no-create-home --shell /usr/sbin/nologin dreamdict

# Build and install binary
go build -o /usr/local/bin/dreamdict ./cmd/server
chmod 755 /usr/local/bin/dreamdict

# Build the database (run as yourself, not the dreamdict user)
mkdir -p /opt/dreamdict /etc/dreamdict
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --data ./data
chown -R dreamdict:dreamdict /opt/dreamdict

# Configure
cat > /etc/dreamdict/dreamdict.env <<EOF
DREAMDICT_PORT=7777
DREAMDICT_DB=/opt/dreamdict/dict.db
DREAMDICT_TOKEN=changeme
EOF
chmod 600 /etc/dreamdict/dreamdict.env

# Install and start
cp deploy/dreamdict.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now dreamdict

# Verify
systemctl status dreamdict
curl localhost:7777/health
```

### Updating the Database

```bash
systemctl stop dreamdict
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --clean
chown dreamdict:dreamdict /opt/dreamdict/dict.db
systemctl start dreamdict
```

---

## Testing

```bash
go test ./...
```

---

## Project Structure

```
cmd/server/           HTTP server binary
cmd/dictimport/       One-time data import CLI
internal/dictionary/  Core package: schema, types, queries
internal/loader/      One loader per data source
scripts/              Data download script
deploy/               systemd unit file
data/                 Source data files (gitignored)
```

---

## Expected Database Size

| Language | Words | Definitions |
|---|---|---|
| en | ~137k | ~362k |
| fr | ~56k | ~138k |
| pt-PT | ~136k | ~85k |
| es† | ~118k | ~151k |
| zh | ~121k | ~200k |
| **Total** | **~568k** | **~936k** |

English words include ~3,800 US-only and ~3,900 GB-only regional variants. Word counts include inflected forms from affix expansion, with ghost words pruned automatically.

† Spanish figures are projections, not measured on a full import. A Spanish import without Wiktionary yields 56k words from the Hunspell list, affix expansion, the frequency list, and 113k OMW synset links; Wiktionary supplies the rest.

---

## License

MIT
