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
echo "=== WordNet (English definitions + synonyms) ==="
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
    # Princeton's official URL is down (403); use GitHub mirror
    download "https://raw.githubusercontent.com/zweiein/WNdb/master/WNdb-3.1.tar.gz" "$WN_TAR"
    echo "  [extract] $WN_TAR"
    mkdir -p "$WN_DIR"
    tar -xzf "$WN_TAR" -C "$WN_DIR"
    save_checksum "wordnet" "$WN_TAR"
    rm -f "$WN_TAR"
    # The archive extracts to WNdb-3.1/dict/ — normalize to wordnet/dict/
    if [[ -d "$WN_DIR/WNdb-3.1/dict" ]]; then
        mv "$WN_DIR/WNdb-3.1/dict" "$WN_DIR/dict"
        rm -rf "$WN_DIR/WNdb-3.1"
    fi
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
# French + pt-PT — Hunspell (LibreOffice dictionaries)
# ============================================================
echo "=== Hunspell (French + Portuguese) ==="
HUNSPELL_DIR="$DATA_DIR/hunspell-dicts"
clone_if_missing "https://github.com/LibreOffice/dictionaries.git" "$HUNSPELL_DIR"
# Copy the files we need and verify they exist
mkdir -p "$DATA_DIR/fr_FR" "$DATA_DIR/pt_PT"
for dic in "$HUNSPELL_DIR/fr_FR/fr.dic" "$HUNSPELL_DIR/pt_PT/pt_PT.dic"; do
    if [[ ! -f "$dic" ]]; then
        echo "  [FAIL] expected file missing from clone: $dic"
        exit 1
    fi
done
cp -f "$HUNSPELL_DIR/fr_FR/fr.dic" "$DATA_DIR/fr_FR/fr.dic"
cp -f "$HUNSPELL_DIR/fr_FR/fr.aff" "$DATA_DIR/fr_FR/fr.aff" 2>/dev/null || true
cp -f "$HUNSPELL_DIR/pt_PT/pt_PT.dic" "$DATA_DIR/pt_PT/pt_PT.dic"

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
