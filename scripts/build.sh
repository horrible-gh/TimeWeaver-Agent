#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
case "${1:-}" in
  "") git pull --ff-only ;;
  --no-pull) ;;
  *) echo "Usage: bash scripts/build.sh [--no-pull]" >&2; exit 2 ;;
esac
command -v go >/dev/null || { echo "Go is required to build." >&2; exit 1; }
mkdir -p dist
TMP="$(mktemp "$ROOT/dist/.timeweaver-agent.XXXXXX")"
trap 'rm -f "$TMP"' EXIT
VERSION="0.1.0-dev"
if SHA="$(git rev-parse --short HEAD 2>/dev/null)"; then VERSION="$VERSION+$SHA"; fi
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$TMP" ./cmd/timeweaver-agent
chmod 755 "$TMP"
mv -f "$TMP" "$ROOT/dist/timeweaver-agent"
echo "Built dist/timeweaver-agent ($VERSION)"
