#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
if [[ ! -x "$ROOT/dist/timeweaver-agent" ]]; then
  echo 'Agent is not built. Run: bash setup.sh' >&2
  exit 1
fi
exec "$ROOT/dist/timeweaver-agent" run "$@"
