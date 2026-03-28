# DreamDict

A self-contained HTTP dictionary service providing word validation, random word generation, definitions, synonyms, and cross-language translation for English (`en`), French (`fr`), and European Portuguese (`pt-PT`).

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

Check if a word exists in a language's word list.

```
$ curl 'localhost:7777/valid?word=ephemeral&lang=en'
{"valid":true}
```

### `GET /random?lang=...`

Return a random word. Optional filters: `pos` (`noun`, `verb`, `adjective`, `adverb`), `min`/`max` (word length), `min_freq` (frequency score, meaningful for `fr`).

```
$ curl 'localhost:7777/random?lang=en&pos=noun&min=5&max=8'
{"word":"lantern"}
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

### `GET /translate?word=...&from=...&to=...`

Return known translations between supported languages.

```
$ curl 'localhost:7777/translate?word=cat&from=en&to=fr'
{"word":"cat","from":"en","to":"fr","translations":["chat","minet"]}
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
| `--log-level` | | `info` | `debug`, `info`, `warn`, `error` |

Flags take precedence over environment variables.

## Import CLI

```bash
go run ./cmd/dictimport [flags]
```

| Flag | Default | Description |
|---|---|---|
| `--db` | `./dict.db` | Output database path |
| `--data` | `./data` | Source data directory |
| `--langs` | `en,fr,pt-PT` | Languages to import |
| `--clean` | `false` | Drop and recreate all tables first |
| `--skip` | | Comma-separated loader names to skip |

Loader names: `scowl`, `wordnet`, `wiktionary-en`, `hunspell-fr`, `lexique`, `wolf`, `wiktionary-fr`, `hunspell-pt`, `dicionario`, `wiktionary-pt`.

## Data Sources

| Source | Language | What it provides |
|---|---|---|
| [SCOWL](https://wordlist.aspell.net/) | en | Word list |
| [WordNet](https://wordnet.princeton.edu/) | en | Definitions + synonyms |
| [Hunspell/LibreOffice](https://github.com/LibreOffice/dictionaries) | fr, pt-PT | Word lists |
| [Lexique](http://www.lexique.org/) | fr | POS + frequency enrichment |
| [WOLF](https://github.com/nicolashernandez/WOLF) | fr | Definitions + synonyms |
| [Dicionario Aberto](https://github.com/jorgecardoso/dicionario-aberto) | pt-PT | Definitions |
| [Wiktionary (kaikki.org)](https://kaikki.org/) | all | Supplemental definitions, synonyms, translations |

Run `./scripts/download-dict-data.sh` to fetch everything. Pass `--force` to re-download existing files.

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
cmd/server/          HTTP server
cmd/dictimport/      One-time data import CLI
internal/dictionary/  Core package: DB schema, types, queries
internal/loader/      Data source parsers (one per source)
scripts/             Download script for source data
deploy/              systemd unit file
data/                Source data files (gitignored)
```

## Expected Database Size

| Language | Words | Definitions | Size |
|---|---|---|---|
| en | ~220k | ~380k | ~180 MB |
| fr | ~90k | ~200k | ~80 MB |
| pt-PT | ~75k | ~120k | ~50 MB |
| **Total** | | | **~310 MB** |
