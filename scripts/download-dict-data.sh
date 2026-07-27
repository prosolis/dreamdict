#!/usr/bin/env bash
set -euo pipefail

DATA_DIR="${1:-./data}"
FORCE="${FORCE:-false}"

if [[ "${1:-}" == "--force" ]] || [[ "${2:-}" == "--force" ]]; then
    FORCE=true
    if [[ "${1:-}" == "--force" ]]; then
        DATA_DIR="./data"
    fi
fi

mkdir -p "$DATA_DIR"

# ---- Integrity helpers ----

# verify_archive <file> — test that a compressed archive is intact.
# Supports .tar.gz, .gz, .bz2. Returns 0 on success.
verify_archive() {
    local file="$1"
    case "$file" in
        *.tar.gz|*.tgz) tar -tzf "$file" >/dev/null 2>&1 ;;
        *.gz)           gunzip -t "$file" 2>/dev/null ;;
        *.bz2)          bunzip2 -t "$file" 2>/dev/null ;;
        *)              return 0 ;;  # unknown format, skip check
    esac
}

# verify_checksum <file> <expected_sha256>
# Returns 0 if the file's SHA-256 matches.
verify_checksum() {
    local file="$1" expected="$2"
    local actual
    actual=$(sha256sum "$file" | awk '{print $1}')
    if [[ "$actual" != "$expected" ]]; then
        echo "  [FAIL] checksum mismatch for $file"
        echo "         expected: $expected"
        echo "         got:      $actual"
        return 1
    fi
    echo "  [ok] checksum verified: $file"
    return 0
}

# check_min_size <file> <min_bytes>
# Returns 0 if file exists and is at least min_bytes.
check_min_size() {
    local file="$1" min="$2"
    if [[ ! -f "$file" ]]; then return 1; fi
    local size
    size=$(stat --printf='%s' "$file" 2>/dev/null || stat -f '%z' "$file" 2>/dev/null || echo 0)
    (( size >= min ))
}

# Known SHA-256 checksums for pinned-version downloads.
# These are verified once and recorded; update when bumping versions.
CHECKSUM_FILE="$DATA_DIR/.checksums"
touch "$CHECKSUM_FILE"

# save_checksum <label> <file> — record sha256 after a successful verified download
save_checksum() {
    local label="$1" file="$2"
    local hash
    hash=$(sha256sum "$file" | awk '{print $1}')
    # Remove old entry, append new
    grep -v "^${label} " "$CHECKSUM_FILE" > "$CHECKSUM_FILE.tmp" 2>/dev/null || true
    echo "${label} ${hash}" >> "$CHECKSUM_FILE.tmp"
    mv "$CHECKSUM_FILE.tmp" "$CHECKSUM_FILE"
}

# check_saved_checksum <label> <file> — verify file matches its recorded checksum
check_saved_checksum() {
    local label="$1" file="$2"
    local expected
    expected=$(grep "^${label} " "$CHECKSUM_FILE" 2>/dev/null | awk '{print $2}')
    if [[ -z "$expected" ]]; then
        echo "  [warn] no saved checksum for $label — will re-download"
        return 1
    fi
    verify_checksum "$file" "$expected"
}

download() {
    local url="$1"
    local dest="$2"
    if [[ -f "$dest" && "$FORCE" != "true" ]]; then
        echo "  [skip] $dest already exists"
        return
    fi
    echo "  [download] $url -> $dest"
    mkdir -p "$(dirname "$dest")"
    curl -fSL --progress-bar -o "$dest" "$url"
    # Verify archive integrity immediately after download
    if verify_archive "$dest"; then
        echo "  [ok] archive integrity verified: $dest"
    else
        echo "  [FAIL] corrupt download, removing: $dest"
        rm -f "$dest"
        return 1
    fi
}

clone_if_missing() {
    local repo="$1"
    local dest="$2"
    if [[ -d "$dest" && "$FORCE" != "true" ]]; then
        echo "  [skip] $dest already exists"
        return
    fi
    echo "  [clone] $repo -> $dest"
    rm -rf "$dest"
    git clone --depth 1 "$repo" "$dest"
}

# ============================================================
# English — SCOWL (latest word list release: 2020.12.07)
# ============================================================
echo "=== SCOWL (English word lists) ==="
SCOWL_DIR="$DATA_DIR/scowl"
SCOWL_OK=false
if [[ -d "$SCOWL_DIR/final" && "$FORCE" != "true" ]]; then
    # Verify extracted data has reasonable content
    scowl_count=$(find "$SCOWL_DIR/final" -type f 2>/dev/null | wc -l)
    if (( scowl_count > 10 )); then
        echo "  [skip] $SCOWL_DIR/final already exists ($scowl_count files)"
        SCOWL_OK=true
    else
        echo "  [warn] $SCOWL_DIR/final looks incomplete ($scowl_count files), re-downloading"
        rm -rf "$SCOWL_DIR"
    fi
fi
if [[ "$SCOWL_OK" != "true" ]]; then
    SCOWL_TAR="$DATA_DIR/scowl.tar.gz"
    download "https://downloads.sourceforge.net/wordlist/scowl-2020.12.07.tar.gz" "$SCOWL_TAR"
    echo "  [extract] $SCOWL_TAR"
    mkdir -p "$SCOWL_DIR"
    tar -xzf "$SCOWL_TAR" -C "$SCOWL_DIR" --strip-components=1
    save_checksum "scowl" "$SCOWL_TAR"
    rm -f "$SCOWL_TAR"
fi

# ============================================================
# English — WordNet
# ============================================================
echo "=== WordNet 3.0 (English definitions + synonyms) ==="
# IMPORTANT: We use WordNet 3.0, NOT 3.1, because WOLF's synset IDs
# use WN 3.0 byte offsets (eng-30-XXXXXXXX). WN 3.1 has different
# offsets, so cross-language synset backing would silently fail.
WN_DIR="$DATA_DIR/wordnet"
if [[ -d "$WN_DIR/dict" && "$FORCE" != "true" ]]; then
    # Verify the dict directory has the expected data files
    if [[ -f "$WN_DIR/dict/data.noun" && -f "$WN_DIR/dict/data.verb" ]]; then
        echo "  [skip] $WN_DIR/dict already exists (data files present)"
    else
        echo "  [warn] $WN_DIR/dict looks incomplete, re-downloading"
        rm -rf "$WN_DIR"
    fi
fi
if [[ ! -d "$WN_DIR/dict" ]]; then
    WN_TAR="$DATA_DIR/wordnet.tar.gz"
    # Use WordNet 3.0 to match WOLF synset offsets
    download "https://wordnetcode.princeton.edu/3.0/WNdb-3.0.tar.gz" "$WN_TAR" 2>/dev/null || {
        echo "  [FAIL] Could not download WordNet 3.0"
        exit 1
    }
    echo "  [extract] $WN_TAR"
    mkdir -p "$WN_DIR"
    tar -xzf "$WN_TAR" -C "$WN_DIR"
    save_checksum "wordnet" "$WN_TAR"
    rm -f "$WN_TAR"
    # Normalize extraction directory — may be WNdb-3.0/ or dict/
    for d in "WNdb-3.0" "WNdb-3.1"; do
        if [[ -d "$WN_DIR/$d/dict" ]]; then
            mv "$WN_DIR/$d/dict" "$WN_DIR/dict"
            rm -rf "$WN_DIR/$d"
            break
        fi
    done
fi

# ============================================================
# English — SUBTLEX-US (word frequency)
# ============================================================
echo "=== SUBTLEX-US (English word frequency) ==="
SUBTLEX_FILE="$DATA_DIR/SUBTLEX-US.tsv"
if [[ -f "$SUBTLEX_FILE" && "$FORCE" != "true" ]]; then
    if check_min_size "$SUBTLEX_FILE" 1000000; then
        echo "  [skip] SUBTLEX-US.tsv already exists (size ok)"
    else
        echo "  [warn] SUBTLEX-US.tsv looks truncated, re-downloading"
        rm -f "$SUBTLEX_FILE"
    fi
fi
if [[ ! -f "$SUBTLEX_FILE" ]]; then
    # Try the GitHub-hosted copy first (TSV, direct download), then Ghent University.
    download "https://raw.githubusercontent.com/Wikipedia2Vec/Wikipedia2Vec/master/resources/SUBTLEX-US.tsv" "$SUBTLEX_FILE" 2>/dev/null || \
    download "https://raw.githubusercontent.com/Wikipedia2Vec/Wikipedia2Vec/refs/heads/master/resources/SUBTLEX-US.tsv" "$SUBTLEX_FILE" 2>/dev/null || true

    if [[ ! -f "$SUBTLEX_FILE" ]]; then
        # Fallback: Ghent University zip (may require browser)
        SUBTLEX_ZIP="$DATA_DIR/subtlex-us.zip"
        download "https://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus/subtlexus2.zip/at_download/file" "$SUBTLEX_ZIP" 2>/dev/null || \
        download "https://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus/subtlexus2.zip" "$SUBTLEX_ZIP" 2>/dev/null || true

        if [[ -f "$SUBTLEX_ZIP" ]]; then
            echo "  [extract] $SUBTLEX_ZIP"
            unzip -qo "$SUBTLEX_ZIP" -d "$DATA_DIR/subtlex-tmp" 2>/dev/null || true
            FOUND_SUBTLEX=$(find "$DATA_DIR/subtlex-tmp" -type f \( -name "*.tsv" -o -name "*.txt" -o -name "*.csv" \) | head -1)
            if [[ -n "$FOUND_SUBTLEX" ]]; then
                mv "$FOUND_SUBTLEX" "$SUBTLEX_FILE"
                echo "  [ok] extracted to SUBTLEX-US.tsv"
            else
                echo "  [warn] could not find TSV in archive — SUBTLEX may need manual conversion from XLSX"
            fi
            rm -rf "$DATA_DIR/subtlex-tmp" "$SUBTLEX_ZIP"
        else
            echo "  [warn] SUBTLEX-US download failed."
            echo "  Download manually from: https://www.ugent.be/pp/experimentele-psychologie/en/research/documents/subtlexus"
            echo "  Export the XLSX to TSV and place as: $SUBTLEX_FILE"
        fi
    fi
fi

# ============================================================
# English — CMU Pronouncing Dictionary
# ============================================================
echo "=== CMUdict (English pronunciation) ==="
CMUDICT_FILE="$DATA_DIR/cmudict-0.7b"
if [[ -f "$CMUDICT_FILE" && "$FORCE" != "true" ]]; then
    if check_min_size "$CMUDICT_FILE" 2000000; then
        echo "  [skip] cmudict-0.7b already exists (size ok)"
    else
        echo "  [warn] cmudict-0.7b looks truncated, re-downloading"
        rm -f "$CMUDICT_FILE"
    fi
fi
if [[ ! -f "$CMUDICT_FILE" ]]; then
    download "https://svn.code.sf.net/p/cmusphinx/code/trunk/cmudict/cmudict-0.7b" "$CMUDICT_FILE" 2>/dev/null || \
    download "https://raw.githubusercontent.com/cmusphinx/cmudict/master/cmudict-0.7b" "$CMUDICT_FILE" 2>/dev/null || \
    download "https://raw.githubusercontent.com/Alexir/CMUdict/master/cmudict-0.7b" "$CMUDICT_FILE" 2>/dev/null || {
        echo "  [warn] CMUdict download failed. Download manually and place at: $CMUDICT_FILE"
    }
fi

# ============================================================
# English — Wiktionary (kaikki)
# ============================================================
echo "=== Wiktionary English (compressed: ~436 MB) ==="
if [[ -f "$DATA_DIR/kaikki-en.jsonl" && "$FORCE" != "true" ]]; then
    # Verify the decompressed file isn't truncated (expect at least 500 MB)
    if check_min_size "$DATA_DIR/kaikki-en.jsonl" 500000000; then
        echo "  [skip] kaikki-en.jsonl already exists (size ok)"
    else
        echo "  [warn] kaikki-en.jsonl looks truncated, re-downloading"
        rm -f "$DATA_DIR/kaikki-en.jsonl"
    fi
fi
if [[ ! -f "$DATA_DIR/kaikki-en.jsonl" ]]; then
    KAIKKI_EN_GZ="$DATA_DIR/kaikki-en.jsonl.gz"
    download "https://kaikki.org/dictionary/English/kaikki.org-dictionary-English.jsonl.gz" "$KAIKKI_EN_GZ"
    echo "  [decompress] kaikki-en.jsonl.gz"
    gunzip -kf "$KAIKKI_EN_GZ"
    save_checksum "kaikki-en" "$KAIKKI_EN_GZ"
fi

# ============================================================
# French + pt-PT + Spanish — Hunspell (LibreOffice dictionaries)
# ============================================================
echo "=== Hunspell (French + Portuguese + Spanish) ==="
HUNSPELL_DIR="$DATA_DIR/hunspell-dicts"
clone_if_missing "https://github.com/LibreOffice/dictionaries.git" "$HUNSPELL_DIR"
# Copy the files we need and verify they exist
mkdir -p "$DATA_DIR/fr_FR" "$DATA_DIR/pt_PT" "$DATA_DIR/es_ES"
for dic in "$HUNSPELL_DIR/fr_FR/fr.dic" "$HUNSPELL_DIR/pt_PT/pt_PT.dic" "$HUNSPELL_DIR/es/es_ES.dic"; do
    if [[ ! -f "$dic" ]]; then
        echo "  [FAIL] expected file missing from clone: $dic"
        exit 1
    fi
done
cp -f "$HUNSPELL_DIR/fr_FR/fr.dic" "$DATA_DIR/fr_FR/fr.dic"
if [[ -f "$HUNSPELL_DIR/fr_FR/fr.aff" ]]; then
    cp -f "$HUNSPELL_DIR/fr_FR/fr.aff" "$DATA_DIR/fr_FR/fr.aff"
else
    FR_AFF=$(find "$HUNSPELL_DIR/fr_FR" -name "*.aff" -type f 2>/dev/null | head -1)
    if [[ -n "$FR_AFF" ]]; then
        cp -f "$FR_AFF" "$DATA_DIR/fr_FR/fr.aff"
        echo "  [ok] found fr .aff file at: $FR_AFF"
    else
        echo "  [warn] no .aff file found for fr — affix expansion will be skipped"
    fi
fi
cp -f "$HUNSPELL_DIR/pt_PT/pt_PT.dic" "$DATA_DIR/pt_PT/pt_PT.dic"
# .aff file may be in pt_PT/ or at a different path in the repo
if [[ -f "$HUNSPELL_DIR/pt_PT/pt_PT.aff" ]]; then
    cp -f "$HUNSPELL_DIR/pt_PT/pt_PT.aff" "$DATA_DIR/pt_PT/pt_PT.aff"
else
    PT_AFF=$(find "$HUNSPELL_DIR/pt_PT" -name "*.aff" -type f 2>/dev/null | head -1)
    if [[ -n "$PT_AFF" ]]; then
        cp -f "$PT_AFF" "$DATA_DIR/pt_PT/pt_PT.aff"
        echo "  [ok] found pt_PT .aff file at: $PT_AFF"
    else
        echo "  [warn] no .aff file found for pt_PT — affix expansion will be skipped"
    fi
fi
# Spanish dictionaries live in es/ in the repo (es_ES, es_MX, es_AR, ...).
# We take es_ES (Castilian) and store it under es_ES/ to match the import paths.
cp -f "$HUNSPELL_DIR/es/es_ES.dic" "$DATA_DIR/es_ES/es_ES.dic"
if [[ -f "$HUNSPELL_DIR/es/es_ES.aff" ]]; then
    cp -f "$HUNSPELL_DIR/es/es_ES.aff" "$DATA_DIR/es_ES/es_ES.aff"
else
    # es/ holds one .aff per regional variant (es_AR, es_MX, ...), so only
    # accept an es_ES one — an arbitrary variant would be the wrong dialect.
    ES_AFF=$(find "$HUNSPELL_DIR" -type f -name "es_ES.aff" 2>/dev/null | head -1)
    if [[ -n "$ES_AFF" ]]; then
        cp -f "$ES_AFF" "$DATA_DIR/es_ES/es_ES.aff"
        echo "  [ok] found es_ES .aff file at: $ES_AFF"
    else
        echo "  [warn] no es_ES .aff file found — affix expansion will be skipped"
    fi
fi

# ============================================================
# French — Lexique
# ============================================================
echo "=== Lexique (French POS + frequency) ==="
if [[ -f "$DATA_DIR/Lexique383.tsv" && "$FORCE" != "true" ]]; then
    # TSV should be at least 10 MB
    if check_min_size "$DATA_DIR/Lexique383.tsv" 10000000; then
        echo "  [skip] Lexique383.tsv already exists (size ok)"
    else
        echo "  [warn] Lexique383.tsv looks truncated, re-downloading"
        rm -f "$DATA_DIR/Lexique383.tsv"
    fi
fi
if [[ ! -f "$DATA_DIR/Lexique383.tsv" ]]; then
    # lexique.org is intermittently unreachable; if this fails, obtain
    # Lexique383.tsv manually and place it in the data directory.
    download "http://www.lexique.org/databases/Lexique383/Lexique383.tsv" "$DATA_DIR/Lexique383.tsv" || {
        echo "  [warn] Lexique download failed — lexique.org may be down."
        echo "  Place Lexique383.tsv in $DATA_DIR manually, or use: pip install pylexique"
    }
fi

# ============================================================
# French — WOLF
# ============================================================
echo "=== WOLF (French definitions + synonyms) ==="
WOLF_BZ2="$DATA_DIR/wolf-1.0b4.xml.bz2"
if [[ -f "$DATA_DIR/wolf.xml" && "$FORCE" != "true" ]]; then
    # WOLF XML should be at least 20 MB
    if check_min_size "$DATA_DIR/wolf.xml" 20000000; then
        echo "  [skip] wolf.xml already exists (size ok)"
    else
        echo "  [warn] wolf.xml looks truncated, re-downloading"
        rm -f "$DATA_DIR/wolf.xml" "$WOLF_BZ2"
    fi
fi
if [[ ! -f "$DATA_DIR/wolf.xml" ]]; then
    download "https://almanach.inria.fr/software_and_resources/downloads/wolf-1.0b4.xml.bz2" "$WOLF_BZ2"
    echo "  [decompress] $WOLF_BZ2"
    bunzip2 -kf "$WOLF_BZ2"
    mv -f "$DATA_DIR/wolf-1.0b4.xml" "$DATA_DIR/wolf.xml" 2>/dev/null || true
    save_checksum "wolf" "$WOLF_BZ2"
fi

# ============================================================
# French — Wiktionary (kaikki)
# ============================================================
echo "=== Wiktionary French (compressed: ~47 MB) ==="
if [[ -f "$DATA_DIR/kaikki-fr.jsonl" && "$FORCE" != "true" ]]; then
    if check_min_size "$DATA_DIR/kaikki-fr.jsonl" 50000000; then
        echo "  [skip] kaikki-fr.jsonl already exists (size ok)"
    else
        echo "  [warn] kaikki-fr.jsonl looks truncated, re-downloading"
        rm -f "$DATA_DIR/kaikki-fr.jsonl"
    fi
fi
if [[ ! -f "$DATA_DIR/kaikki-fr.jsonl" ]]; then
    KAIKKI_FR_GZ="$DATA_DIR/kaikki-fr.jsonl.gz"
    download "https://kaikki.org/dictionary/French/kaikki.org-dictionary-French.jsonl.gz" "$KAIKKI_FR_GZ"
    echo "  [decompress] kaikki-fr.jsonl.gz"
    gunzip -kf "$KAIKKI_FR_GZ"
    save_checksum "kaikki-fr" "$KAIKKI_FR_GZ"
fi

# ============================================================
# pt-PT — Dicionário Aberto
# ============================================================
echo "=== Dicionário Aberto (Portuguese definitions) ==="
DICIO_DIR="$DATA_DIR/dicionario-aberto-repo"
if [[ -f "$DATA_DIR/dicionario-aberto.xml" && "$FORCE" != "true" ]]; then
    if check_min_size "$DATA_DIR/dicionario-aberto.xml" 1000000; then
        echo "  [skip] dicionario-aberto.xml already exists (size ok)"
    else
        echo "  [warn] dicionario-aberto.xml looks truncated, re-downloading"
        rm -f "$DATA_DIR/dicionario-aberto.xml"
        rm -rf "$DICIO_DIR"
    fi
fi
if [[ ! -f "$DATA_DIR/dicionario-aberto.xml" ]]; then
    clone_if_missing "https://github.com/ambs/Dicionario-Aberto.git" "$DICIO_DIR"
    # Data lives in Resources/xmls.zip as per-letter XML files; extract and merge
    DICIO_ZIP="$DICIO_DIR/Resources/xmls.zip"
    if [[ ! -f "$DICIO_ZIP" ]]; then
        echo "  [FAIL] expected $DICIO_ZIP not found in clone"
        exit 1
    fi
    DICIO_TMP="$DATA_DIR/dicionario-aberto-xmls"
    echo "  [extract] $DICIO_ZIP"
    unzip -qo "$DICIO_ZIP" -d "$DICIO_TMP"
    echo "  [merge] combining per-letter XML files into dicionario-aberto.xml"
    echo '<?xml version="1.0"?>' > "$DATA_DIR/dicionario-aberto.xml"
    echo '<dic>' >> "$DATA_DIR/dicionario-aberto.xml"
    for xmlfile in "$DICIO_TMP"/xmls/*.xml; do
        # Extract entry elements, skipping the <?xml?>, <dic>, <head> wrappers
        sed -n '/<entry /,/<\/entry>/p' "$xmlfile" >> "$DATA_DIR/dicionario-aberto.xml"
    done
    echo '</dic>' >> "$DATA_DIR/dicionario-aberto.xml"
    rm -rf "$DICIO_TMP" "$DICIO_DIR"
    if ! check_min_size "$DATA_DIR/dicionario-aberto.xml" 1000000; then
        echo "  [FAIL] dicionario-aberto.xml seems too small after merge"
        exit 1
    fi
fi

# ============================================================
# pt-PT — CETEMPúblico frequency (or fallback frequency list)
# ============================================================
echo "=== Portuguese frequency data ==="
PT_FREQ_FILE="$DATA_DIR/pt_50k.txt"
if [[ -f "$PT_FREQ_FILE" && "$FORCE" != "true" ]]; then
    if check_min_size "$PT_FREQ_FILE" 100000; then
        echo "  [skip] pt_50k.txt already exists (size ok)"
    else
        echo "  [warn] pt_50k.txt looks truncated, re-downloading"
        rm -f "$PT_FREQ_FILE"
    fi
fi
if [[ ! -f "$PT_FREQ_FILE" ]]; then
    # OpenSubtitles-derived frequency list (hermitdave/FrequencyWords)
    download "https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2016/pt/pt_50k.txt" "$PT_FREQ_FILE" 2>/dev/null || {
        echo "  [warn] Portuguese frequency download failed."
        echo "  The import will proceed without frequency data."
    }
fi

# ============================================================
# pt-PT + Spanish — Open Multilingual Wordnet
# ============================================================
echo "=== Open Multilingual Wordnet (Portuguese + Spanish synset mappings) ==="
OMW_DIR="$DATA_DIR/omw"
# ISO 639-3 code : language label used in messages
OMW_LANGS=("por:Portuguese:pt-PT" "spa:Spanish:es")
OMW_MISSING=false
for entry in "${OMW_LANGS[@]}"; do
    code="${entry%%:*}"
    if [[ -f "$OMW_DIR/wn-data-$code.tab" && "$FORCE" != "true" ]]; then
        echo "  [skip] wn-data-$code.tab already exists"
    else
        OMW_MISSING=true
    fi
done
if [[ "$OMW_MISSING" == "true" ]]; then
    OMW_REPO="$DATA_DIR/omw-data-repo"
    clone_if_missing "https://github.com/omwn/omw-data.git" "$OMW_REPO"
    mkdir -p "$OMW_DIR"
    for entry in "${OMW_LANGS[@]}"; do
        code="${entry%%:*}"
        rest="${entry#*:}"
        label="${rest%%:*}"
        langtag="${rest#*:}"
        OMW_FILE="$OMW_DIR/wn-data-$code.tab"
        if [[ -f "$OMW_FILE" && "$FORCE" != "true" ]]; then
            continue
        fi
        # The MCR tab files live under wns/mcr/ but the layout has moved
        # between releases, so search the whole clone.
        FOUND_OMW=$(find "$OMW_REPO" -type f -name "wn-data-$code.tab" 2>/dev/null | head -1)
        if [[ -z "$FOUND_OMW" ]]; then
            # Try alternative naming
            FOUND_OMW=$(find "$OMW_REPO" -type f -name "*$code*" -name "*.tab" 2>/dev/null | head -1)
        fi
        if [[ -n "$FOUND_OMW" ]]; then
            cp "$FOUND_OMW" "$OMW_FILE"
            echo "  [ok] extracted $label OMW data (wn-data-$code.tab)"
        else
            echo "  [warn] could not find $label data in OMW repo"
            echo "  The import will proceed without synset mappings for $langtag."
        fi
    done
    rm -rf "$OMW_REPO"
fi

# ============================================================
# pt-PT — Wiktionary (kaikki)
# ============================================================
echo "=== Wiktionary Portuguese (compressed: ~45 MB) ==="
if [[ -f "$DATA_DIR/kaikki-pt.jsonl" && "$FORCE" != "true" ]]; then
    if check_min_size "$DATA_DIR/kaikki-pt.jsonl" 50000000; then
        echo "  [skip] kaikki-pt.jsonl already exists (size ok)"
    else
        echo "  [warn] kaikki-pt.jsonl looks truncated, re-downloading"
        rm -f "$DATA_DIR/kaikki-pt.jsonl"
    fi
fi
if [[ ! -f "$DATA_DIR/kaikki-pt.jsonl" ]]; then
    KAIKKI_PT_GZ="$DATA_DIR/kaikki-pt.jsonl.gz"
    download "https://kaikki.org/dictionary/Portuguese/kaikki.org-dictionary-Portuguese.jsonl.gz" "$KAIKKI_PT_GZ"
    echo "  [decompress] kaikki-pt.jsonl.gz"
    gunzip -kf "$KAIKKI_PT_GZ"
    save_checksum "kaikki-pt" "$KAIKKI_PT_GZ"
fi

# ============================================================
# Spanish — frequency list
# ============================================================
echo "=== Spanish frequency data ==="
ES_FREQ_FILE="$DATA_DIR/es_50k.txt"
if [[ -f "$ES_FREQ_FILE" && "$FORCE" != "true" ]]; then
    if check_min_size "$ES_FREQ_FILE" 100000; then
        echo "  [skip] es_50k.txt already exists (size ok)"
    else
        echo "  [warn] es_50k.txt looks truncated, re-downloading"
        rm -f "$ES_FREQ_FILE"
    fi
fi
if [[ ! -f "$ES_FREQ_FILE" ]]; then
    # OpenSubtitles-derived frequency list (hermitdave/FrequencyWords)
    download "https://raw.githubusercontent.com/hermitdave/FrequencyWords/master/content/2016/es/es_50k.txt" "$ES_FREQ_FILE" 2>/dev/null || {
        echo "  [warn] Spanish frequency download failed."
        echo "  The import will proceed without frequency data."
    }
fi

# ============================================================
# Spanish — Wiktionary (kaikki)
# ============================================================
echo "=== Wiktionary Spanish (compressed: ~90 MB) ==="
if [[ -f "$DATA_DIR/kaikki-es.jsonl" && "$FORCE" != "true" ]]; then
    if check_min_size "$DATA_DIR/kaikki-es.jsonl" 50000000; then
        echo "  [skip] kaikki-es.jsonl already exists (size ok)"
    else
        echo "  [warn] kaikki-es.jsonl looks truncated, re-downloading"
        rm -f "$DATA_DIR/kaikki-es.jsonl"
    fi
fi
if [[ ! -f "$DATA_DIR/kaikki-es.jsonl" ]]; then
    KAIKKI_ES_GZ="$DATA_DIR/kaikki-es.jsonl.gz"
    download "https://kaikki.org/dictionary/Spanish/kaikki.org-dictionary-Spanish.jsonl.gz" "$KAIKKI_ES_GZ"
    echo "  [decompress] kaikki-es.jsonl.gz"
    gunzip -kf "$KAIKKI_ES_GZ"
    save_checksum "kaikki-es" "$KAIKKI_ES_GZ"
fi

# ============================================================
# Mandarin — CC-CEDICT
# ============================================================
echo "=== CC-CEDICT (Mandarin word list + definitions) ==="
if [[ -f "$DATA_DIR/cedict_ts.u8" && "$FORCE" != "true" ]]; then
    # Expect at least 3 MB
    if check_min_size "$DATA_DIR/cedict_ts.u8" 3000000; then
        echo "  [skip] cedict_ts.u8 already exists (size ok)"
    else
        echo "  [warn] cedict_ts.u8 looks truncated, re-downloading"
        rm -f "$DATA_DIR/cedict_ts.u8"
    fi
fi
if [[ ! -f "$DATA_DIR/cedict_ts.u8" ]]; then
    CEDICT_GZ="$DATA_DIR/cedict_ts.u8.gz"
    download "https://www.mdbg.net/chinese/export/cedict/cedict_1_0_ts_utf-8_mdbg.txt.gz" "$CEDICT_GZ"
    echo "  [decompress] cedict_ts.u8.gz"
    gunzip -kf "$CEDICT_GZ"
    save_checksum "cedict" "$CEDICT_GZ"
fi

# ============================================================
# Mandarin — Wiktionary (kaikki)
# ============================================================
echo "=== Wiktionary Chinese (compressed) ==="
if [[ -f "$DATA_DIR/kaikki-zh.jsonl" && "$FORCE" != "true" ]]; then
    if check_min_size "$DATA_DIR/kaikki-zh.jsonl" 5000000; then
        echo "  [skip] kaikki-zh.jsonl already exists (size ok)"
    else
        echo "  [warn] kaikki-zh.jsonl looks truncated, re-downloading"
        rm -f "$DATA_DIR/kaikki-zh.jsonl"
    fi
fi
if [[ ! -f "$DATA_DIR/kaikki-zh.jsonl" ]]; then
    KAIKKI_ZH_GZ="$DATA_DIR/kaikki-zh.jsonl.gz"
    download "https://kaikki.org/dictionary/Chinese/kaikki.org-dictionary-Chinese.jsonl.gz" "$KAIKKI_ZH_GZ"
    echo "  [decompress] kaikki-zh.jsonl.gz"
    gunzip -kf "$KAIKKI_ZH_GZ"
    save_checksum "kaikki-zh" "$KAIKKI_ZH_GZ"
fi

echo ""
echo "=== All downloads complete ==="
echo "Data directory: $DATA_DIR"
du -sh "$DATA_DIR" 2>/dev/null || true
