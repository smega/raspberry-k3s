#!/usr/bin/env bash
# Renders og.html into public/og.png (1200x630) with headless Chrome.
# Usage: frontend/og/render.sh   (macOS, Google Chrome installed)

set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHROME="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"

mkdir -p "$DIR/../public"
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
  --window-size=1200,630 --screenshot="$DIR/../public/og.png" "file://$DIR/og.html"
echo "Wrote $DIR/../public/og.png"
