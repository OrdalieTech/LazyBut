#!/usr/bin/env bash
# Builds lazybut, renders the demo workspace as a clean PNG hero shot, and
# saves it to docs/screenshots/hero.png.
#
# Dependencies:
#   - go (to build lazybut)
#   - aha          → brew install aha
#   - macOS Google Chrome at /Applications/Google Chrome.app
#                  (used in headless mode for clean PNG capture)

set -euo pipefail

cd "$(dirname "$0")/.."

OUT="docs/screenshots"
TMP="$(mktemp -d -t lazybut-shots.XXXXXX)"
BIN="$TMP/lazybut"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"

mkdir -p "$OUT"

echo "→ building lazybut"
go build -o "$BIN" ./cmd/lazybut

if ! command -v aha >/dev/null 2>&1; then
  echo "  aha not found — install it with: brew install aha" >&2
  exit 1
fi
if [ ! -x "$CHROME" ]; then
  echo "  chrome not found at $CHROME (set \$CHROME)" >&2
  exit 1
fi

echo "→ rendering demo workspace"
"$BIN" -snapshot 180x44 -snapshot-overlay demo \
  | aha --black --title "lazybut · stardust" > "$TMP/hero.html"

"$CHROME" --headless --disable-gpu \
  --window-size=1600,820 \
  --screenshot="$OUT/hero.png" \
  "file://$TMP/hero.html" 2>/dev/null

echo "✓ wrote $OUT/hero.png"
ls -lh "$OUT/hero.png"
