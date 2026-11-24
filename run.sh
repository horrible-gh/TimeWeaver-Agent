#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"
if [[ ! -x "$ROOT/dist/timeweaver-agent" ]]; then
  echo '[run] Agent is not built; building it from the current checkout...' >&2
  bash scripts/build.sh --no-pull
fi
exec "$ROOT/dist/timeweaver-agent" run "$@"
