#!/bin/bash
# Run this from inside the cloned repository.
# It moves the competition webapp into this repo and renames the repo dir to "webapp".
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISUCON_HOME="$(cd "$REPO_DIR/.." && pwd)"
WEBAPP_ORIG="$ISUCON_HOME/webapp"
WEBAPP_BK="${WEBAPP_ORIG}_bk"
MAKEFILE="$REPO_DIR/Makefile"

# Language directories recognized as "per-language app code"
LANG_CANDIDATES=(go golang perl ruby python python3 node nodejs rust php java)

echo "================================================"
echo " ISUCON Setup"
echo "================================================"
echo " repo    : $REPO_DIR"
echo " webapp  : $WEBAPP_ORIG"
echo "================================================"
echo ""

# ── Pre-flight checks ────────────────────────────────────────────────────────

if [ ! -d "$WEBAPP_ORIG" ]; then
    echo "[ERROR] webapp not found: $WEBAPP_ORIG"
    exit 1
fi

if [ -d "$WEBAPP_BK" ]; then
    echo "[WARN] Backup already exists: $WEBAPP_BK"
    read -rp "       Overwrite? [y/N]: " ans
    [[ "$ans" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 1; }
    rm -rf "$WEBAPP_BK"
fi

# ── Step 1: Backup ───────────────────────────────────────────────────────────

echo "=== Step 1: Backup webapp ==="
cp -r "$WEBAPP_ORIG" "$WEBAPP_BK"
echo "[OK] $WEBAPP_BK"
echo ""

# ── Step 2: Language directory selection ─────────────────────────────────────

echo "=== Step 2: Language directories ==="
FOUND_LANGS=()
for lang in "${LANG_CANDIDATES[@]}"; do
    [ -d "$WEBAPP_ORIG/$lang" ] && FOUND_LANGS+=("$lang")
done

KEEP_LANG=""
if [ "${#FOUND_LANGS[@]}" -eq 0 ]; then
    echo "[INFO] No language directories detected."
elif [ "${#FOUND_LANGS[@]}" -eq 1 ]; then
    KEEP_LANG="${FOUND_LANGS[0]}"
    echo "[INFO] Found only '$KEEP_LANG' — keeping it."
else
    echo "Detected language directories:"
    for i in "${!FOUND_LANGS[@]}"; do
        echo "  [$i] ${FOUND_LANGS[$i]}"
    done
    while true; do
        read -rp "Which language do you use? (enter number): " idx
        if [[ "$idx" =~ ^[0-9]+$ ]] && [ "$idx" -lt "${#FOUND_LANGS[@]}" ]; then
            KEEP_LANG="${FOUND_LANGS[$idx]}"
            break
        fi
        echo "  Please enter a number between 0 and $((${#FOUND_LANGS[@]}-1))."
    done
    echo ""
    echo "Removing other language directories:"
    for lang in "${FOUND_LANGS[@]}"; do
        if [ "$lang" != "$KEEP_LANG" ]; then
            read -rp "  Remove '$lang'? [Y/n]: " ans
            if [[ ! "$ans" =~ ^[Nn]$ ]]; then
                rm -rf "$WEBAPP_ORIG/$lang"
                echo "  [DEL] $lang"
            else
                echo "  [SKIP] $lang"
            fi
        fi
    done
fi
echo ""

# ── Step 3: Other directories / files ────────────────────────────────────────

echo "=== Step 3: Other directories and files ==="
echo "For each item you can:"
echo "  [k] keep as-is"
echo "  [d] delete (will not be included in the repo)"
echo "  [i] add to .gitignore (included in repo but not tracked)"
echo ""

GITIGNORE_ADDS=()

# Collect items first so inner read -rp can use stdin (the terminal) normally
mapfile -t WEBAPP_ITEMS < <(find "$WEBAPP_ORIG" -maxdepth 1 -mindepth 1 | sort)

for item in "${WEBAPP_ITEMS[@]}"; do
    name="$(basename "$item")"

    # Skip language dir we already decided to keep
    [ "$name" = "$KEEP_LANG" ] && continue

    size="$(du -sh "$item" 2>/dev/null | cut -f1)"
    type_label="dir"
    [ -f "$item" ] && type_label="file"

    while true; do
        read -rp "  $type_label '$name' ($size) [k/d/i]: " ans
        case "$ans" in
            d|D)
                rm -rf "$item"
                echo "    [DEL] $name"
                break
                ;;
            i|I)
                GITIGNORE_ADDS+=("$name")
                echo "    [.gitignore] $name"
                break
                ;;
            k|K|"")
                echo "    [KEEP] $name"
                break
                ;;
            *)
                echo "    Please enter k, d, or i."
                ;;
        esac
    done
done
echo ""

# ── Step 4: Makefile configuration ───────────────────────────────────────────

echo "=== Step 4: Makefile ==="

read -rp "APPNAME (systemd service name) [isuhoge-go.service]: " appname
appname="${appname:-isuhoge-go.service}"

echo ""
echo "build command:"
echo "  [0] cd <lang> && go build -o <binary>"
echo "  [1] cd <lang> && make"
echo "  [2] custom"
read -rp "Select [0]: " build_choice
build_choice="${build_choice:-0}"

lang_dir="${KEEP_LANG:-go}"
BUILD_CMD=""
case "$build_choice" in
    1)
        BUILD_CMD="cd $lang_dir && make"
        ;;
    2)
        read -rp "Build command: " BUILD_CMD
        ;;
    *)
        default_bin="${appname%.service}"
        read -rp "Binary output name [$default_bin]: " user_bin
        user_bin="${user_bin:-$default_bin}"
        BUILD_CMD="cd $lang_dir && go build -o $user_bin"
        ;;
esac
echo ""

# ── Step 5: Move webapp contents into the repo ───────────────────────────────

echo "=== Step 5: Move webapp → repo ==="
shopt -s dotglob nullglob
for item in "$WEBAPP_ORIG"/*; do
    name="$(basename "$item")"
    if [ -e "$REPO_DIR/$name" ]; then
        echo "  [SKIP] '$name' already exists in repo"
    else
        mv "$item" "$REPO_DIR/"
        echo "  [MOVE] $name"
    fi
done
shopt -u dotglob nullglob
rmdir "$WEBAPP_ORIG" 2>/dev/null && echo "  [DEL] (empty) $WEBAPP_ORIG" || \
    echo "  [WARN] $WEBAPP_ORIG not empty — check manually"
echo ""

# ── Step 6: Update .gitignore ─────────────────────────────────────────────────

if [ "${#GITIGNORE_ADDS[@]}" -gt 0 ]; then
    echo "=== Step 6: .gitignore ==="
    GITIGNORE_FILE="$REPO_DIR/.gitignore"
    touch "$GITIGNORE_FILE"
    for entry in "${GITIGNORE_ADDS[@]}"; do
        pattern="${entry%/}/"
        if grep -qxF "$pattern" "$GITIGNORE_FILE" 2>/dev/null || \
           grep -qxF "${entry}" "$GITIGNORE_FILE" 2>/dev/null; then
            echo "  [SKIP] $pattern (already in .gitignore)"
        else
            echo "$pattern" >> "$GITIGNORE_FILE"
            echo "  [ADD] $pattern"
        fi
    done
    echo ""
fi

# ── Step 7: Rewrite Makefile ─────────────────────────────────────────────────

echo "=== Step 7: Makefile ==="
# APPNAME
sed -i "s|^APPNAME := .*|APPNAME := $appname|" "$MAKEFILE"
# build command: replace __FIXME__ (tab-prefixed)
if [ -n "$BUILD_CMD" ]; then
    escaped=$(printf '%s' "$BUILD_CMD" | sed 's/[&\]/\\&/g')
    sed -i "s|\t__FIXME__|\t${escaped}|" "$MAKEFILE"
fi
echo "[OK] APPNAME = $appname"
echo "[OK] build   = $BUILD_CMD"
echo ""

# ── Step 8: Rename repo dir to webapp ────────────────────────────────────────

echo "=== Step 8: Rename repo ==="
echo "Renaming $REPO_DIR → $WEBAPP_ORIG ..."
mv "$REPO_DIR" "$WEBAPP_ORIG"
echo "[OK] Repository is now at: $WEBAPP_ORIG"
echo ""
echo "================================================"
echo " Setup complete!"
echo "================================================"
