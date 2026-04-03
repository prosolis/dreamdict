# DreamDict

A self-contained HTTP dictionary service providing word validation, random word generation, definitions, synonyms, antonyms, translations, pronunciation, etymology, and cross-language semantic backing for English (`en`), French (`fr`), European Portuguese (`pt-PT`), and Mandarin Chinese (`zh`).

Runs as a plain Go binary managed by systemd. Binds to localhost only — no external network exposure. Designed as a dictionary backend for [GogoBee](https://github.com/reala-misaki/gogobee), replacing Wordnik.

## Requirements

- Go 1.25+
- No CGO required (uses [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite))

## Quick Start

```bash
# Download all dictionary source data (~2-3 GB)
./scripts/download-dict-data.sh

# Build the database (~310 MB)
go run ./cmd/dictimport --data ./data --db ./dict.db

# Start the server
go run ./cmd/server --db ./dict.db
```

The server listens on `127.0.0.1:7777` by default.

## API

All endpoints are read-only `GET` requests returning JSON.

### `GET /valid?word=...&lang=...`

Check if a word exists in a language's word list. Includes inflected forms via affix expansion.

```
$ curl 'localhost:7777/valid?word=running&lang=en'
{"valid":true}
```

### `GET /random?lang=...`

Return a random word. Optional filters: `pos` (`noun`, `verb`, `adjective`, `adverb`), `min`/`max` (word length), `min_freq` (frequency score), `min_difficulty`/`max_difficulty` (0.0–1.0).

```
$ curl 'localhost:7777/random?lang=en&pos=noun&min=5&max=8'
{"word":"lantern","difficulty":0.42}
```

### `GET /define?word=...&lang=...`

Return all definitions, ordered by source priority.

```
$ curl 'localhost:7777/define?word=ephemeral&lang=en'
{"word":"ephemeral","lang":"en","definitions":[
  {"pos":"adjective","gloss":"lasting for a very short time","source":"wordnet","priority":10},
  {"pos":"adjective","gloss":"lasting only for a day","source":"wiktionary","priority":20}
]}
```

### `GET /synonyms?word=...&lang=...`

Return deduplicated synonyms.

```
$ curl 'localhost:7777/synonyms?word=happy&lang=en'
{"word":"happy","lang":"en","synonyms":["content","glad","joyful","pleased"]}
```

### `GET /antonyms?word=...&lang=...`

Return antonyms sourced from WordNet and Wiktionary.

```
$ curl 'localhost:7777/antonyms?word=happy&lang=en'
{"word":"happy","lang":"en","antonyms":["sad","unhappy"]}
```

### `GET /translate?word=...&from=...&to=...`

Return known translations between supported languages.

```
$ curl 'localhost:7777/translate?word=cat&from=en&to=fr'
{"word":"cat","from":"en","to":"fr","translations":["chat","minet"]}
```

### `GET /backing?word=...&lang=...`

Return English semantic equivalents for a non-English word via shared WordNet synset IDs. Falls back to the translations table when no synset mapping exists.

```
$ curl 'localhost:7777/backing?word=éphémère&lang=fr'
{"word":"éphémère","lang":"fr","equivalents":[
  {"word":"ephemeral","definition":"lasting a very short time","synset":"00914031-a"}
]}
```

### `GET /pronunciation?word=...&lang=...`

Return pronunciation data (CMU phonemes for English, IPA from Wiktionary for all languages).

```
$ curl 'localhost:7777/pronunciation?word=ephemeral&lang=en'
{"word":"ephemeral","lang":"en","pronunciations":[
  {"format":"cmu","value":"IH0 F EH1 M ER0 AH0 L","source":"cmudict"},
  {"format":"ipa","value":"ɪˈfɛm.ər.əl","source":"wiktionary"}
]}
```

### `GET /etymology?word=...&lang=...`

Return the etymology of a word from Wiktionary.

```
$ curl 'localhost:7777/etymology?word=ephemeral&lang=en'
{"word":"ephemeral","lang":"en","etymology":"From Medieval Latin ephemerus, from Ancient Greek ἐφήμερος (ephḗmeros, \"lasting only a day\")..."}
```

### `GET /frequency?word=...&lang=...`

Return the frequency score for a word. Higher values = more common.

```
$ curl 'localhost:7777/frequency?word=house&lang=en'
{"word":"house","lang":"en","frequency":800}
```

### `GET /frequency/batch?words=...&lang=...`

Batch frequency lookup. Comma-separated words, max 100.

```
$ curl 'localhost:7777/frequency/batch?words=house,cat,ephemeral&lang=en'
{"lang":"en","frequencies":{"cat":800,"ephemeral":50,"house":800}}
```

### `GET /health`

Health check with database stats.

```
$ curl localhost:7777/health
{"status":"ok","db_path":"./dict.db","word_counts":{"en":214832,"fr":82104,"pt-PT":71088},...}
```

### Error Responses

```json
{"error": "bad_request", "message": "word and lang are required"}
{"error": "no_match", "message": "no words match the given filters"}
```

## Server Configuration

| Flag | Env Var | Default | Description |
|---|---|---|---|
| `--port` | `DREAMDICT_PORT` | `7777` | Listen port |
| `--host` | `DREAMDICT_HOST` | `127.0.0.1` | Listen host |
| `--db` | `DREAMDICT_DB` | `./dict.db` | Path to SQLite database |
| `--token` | `DREAMDICT_TOKEN` | _(none)_ | Bearer token for API auth |
| `--log-level` | | `info` | `debug`, `info`, `warn`, `error` |

Flags take precedence over environment variables.

### Authentication

When `--token` or `DREAMDICT_TOKEN` is set, all endpoints except `/health` require a bearer token:

```bash
# Start with auth enabled
DREAMDICT_TOKEN=mysecret go run ./cmd/server --db ./dict.db

# Authenticated request
curl -H 'Authorization: Bearer mysecret' 'localhost:7777/define?word=casa&lang=pt-PT'

# Health is always public (for monitoring)
curl localhost:7777/health
```

Requests without a valid token receive a `401` response:

```json
{"error": "unauthorized", "message": "invalid or missing bearer token"}
```

When no token is configured, auth is disabled and all endpoints are open.

## Import CLI

```bash
go run ./cmd/dictimport [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--db` | `./dict.db` | Output database path |
| `--data` | `./data` | Source data directory |
| `--langs` | `en,fr,pt-PT,zh` | Languages to import |
| `--clean` | `false` | Drop and recreate all tables first |
| `--skip` | | Comma-separated loader names to skip |

### Loader Names

| Loader | Language | What it does |
|---|---|---|
| `scowl` | en | English word list with frequency tiers |
| `affix-en` | en | English inflected form expansion |
| `wordnet` | en | Definitions, synonyms, antonyms, synset mappings |
| `subtlex` | en | SUBTLEX-US frequency enrichment |
| `cmudict` | en | CMU pronunciation data |
| `wiktionary-en` | en | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `hunspell-fr` | fr | French word list |
| `affix-fr` | fr | French inflected form expansion |
| `lexique` | fr | French POS + frequency enrichment |
| `wolf` | fr | French definitions, synonyms, synset mappings |
| `wiktionary-fr` | fr | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `hunspell-pt` | pt-PT | Portuguese word list |
| `affix-pt-PT` | pt-PT | Portuguese inflected form expansion |
| `dicionario` | pt-PT | Portuguese definitions |
| `cetempublico` | pt-PT | Portuguese frequency enrichment |
| `omw-pt` | pt-PT | Open Multilingual Wordnet synset mappings |
| `wiktionary-pt` | pt-PT | Supplemental definitions, synonyms, antonyms, translations, IPA, etymology |
| `cedict` | zh | Chinese words, definitions, translations |
| `wiktionary-zh` | zh | Supplemental Chinese data |
| `difficulty` | all | Composite difficulty scoring (runs last) |

## Data Sources

| Source | Language | What it provides |
|---|---|---|
| [SCOWL](https://wordlist.aspell.net/) | en | Word list + frequency tiers |
| [WordNet](https://wordnet.princeton.edu/) | en | Definitions, synonyms, antonyms, synset IDs |
| [SUBTLEX-US](http://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus) | en | Subtitle-based word frequency |
| [CMU Pronouncing Dictionary](http://www.speech.cs.cmu.edu/cgi-bin/cmudict) | en | Phoneme-based pronunciation |
| [Hunspell/LibreOffice](https://github.com/LibreOffice/dictionaries) | fr, pt-PT | Word lists + affix rules |
| [Lexique](http://www.lexique.org/) | fr | POS + frequency enrichment |
| [WOLF](https://github.com/nicolashernandez/WOLF) | fr | Definitions, synonyms, synset IDs |
| [Dicionario Aberto](https://github.com/jorgecardoso/dicionario-aberto) | pt-PT | Definitions |
| [CETEMPúblico](https://www.linguateca.pt/acesso/corpus.php?corpus=CETEMPUBLICO) | pt-PT | Word frequency |
| [Open Multilingual Wordnet](https://github.com/omwn/omw-data) | pt-PT | Synset mappings to Princeton WordNet |
| [CC-CEDICT](https://cc-cedict.org/) | zh | Words, definitions, translations |
| [Wiktionary (kaikki.org)](https://kaikki.org/) | all | Definitions, synonyms, antonyms, translations, IPA pronunciation, etymology |

Run `./scripts/download-dict-data.sh` to fetch everything. Pass `--force` to re-download existing files.

## Database Schema (v2)

### Tables

| Table | Purpose |
|---|---|
| `words` | Core word inventory (word, lang, pos, frequency, difficulty) |
| `definitions` | Word definitions with source priority |
| `synonyms` | Synonym relationships |
| `antonyms` | Antonym relationships |
| `translations` | Cross-language translations |
| `synsets` | Princeton WordNet synset IDs |
| `word_synsets` | Maps words to synsets (for cross-language semantic backing) |
| `pronunciations` | CMU phonemes and IPA data |
| `etymology` | Word origin text from Wiktionary |
| `meta` | Import metadata (imported_at, schema_version) |

### Difficulty Scoring

Each word receives a composite difficulty score (0.0 = easiest, 1.0 = hardest) computed at import time:

```
difficulty = inverse_frequency * 0.5 + word_length * 0.3 + syllable_count * 0.2
```

Use `min_difficulty` / `max_difficulty` on `/random` to select words by difficulty tier.

## Deployment

A systemd unit file is provided at `deploy/dreamdict.service`.

```bash
# Create system user
useradd --system --no-create-home --shell /usr/sbin/nologin dreamdict

# Build and install
go build -o /usr/local/bin/dreamdict ./cmd/server
mkdir -p /opt/dreamdict /etc/dreamdict
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --data ./data
chown -R dreamdict:dreamdict /opt/dreamdict

# Install service
cp deploy/dreamdict.service /etc/systemd/system/
echo 'DREAMDICT_PORT=7777' > /etc/dreamdict/dreamdict.env
echo 'DREAMDICT_DB=/opt/dreamdict/dict.db' >> /etc/dreamdict/dreamdict.env
echo 'DREAMDICT_TOKEN=changeme' >> /etc/dreamdict/dreamdict.env
chmod 600 /etc/dreamdict/dreamdict.env
systemctl daemon-reload
systemctl enable --now dreamdict
```

### Updating the Database

```bash
systemctl stop dreamdict
go run ./cmd/dictimport --db /opt/dreamdict/dict.db --clean
chown dreamdict:dreamdict /opt/dreamdict/dict.db
systemctl start dreamdict
```

## Testing

```bash
go test ./...
```

## Project Structure

```
cmd/server/           HTTP server (12 endpoints)
cmd/dictimport/       One-time data import CLI
internal/dictionary/  Core package: DB schema, types, queries
internal/loader/      Data source parsers (one per source)
scripts/              Download script for source data
deploy/               systemd unit file
data/                 Source data files (gitignored)
```

## Expected Database Size

| Language | Words | Definitions | Size |
|---|---|---|---|
| en | ~220k+ | ~380k | ~180 MB |
| fr | ~90k+ | ~200k | ~80 MB |
| pt-PT | ~75k+ | ~120k | ~50 MB |
| zh | varies | ~80k | ~20 MB |
| **Total** | | | **~330 MB** |

Word counts are higher than base forms due to affix expansion of inflected forms.
