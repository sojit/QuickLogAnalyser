#!/usr/bin/env bash
# Development or release build. Default: development.
# Release: ./scripts/build.sh --release  (validates Git BEFORE any file mutation)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/build_metadata.sh
source "$ROOT/scripts/lib/build_metadata.sh"

MODE=development
PREFLIGHT_ONLY=0
if [[ "${1:-}" == "--release" ]]; then
  MODE=release
  shift || true
fi
if [[ "${1:-}" == "--preflight-only" ]]; then
  PREFLIGHT_ONLY=1
fi

cd "$ROOT"
qa_prepare_identity "$ROOT" "$MODE"
mkdir -p dist
qa_write_identity_json "$ROOT/dist/build-identity.json"
echo "Identity version=$QA_VERSION commit=$QA_COMMIT dirty=$QA_DIRTY build_time=$QA_BUILD_TIME mode=$QA_MODE"

if [[ "$PREFLIGHT_ONLY" -eq 1 ]]; then
  echo "PreflightOnly: skipping go build"
  exit 0
fi

ldflags="$(qa_ldflags)"
echo "Building ($MODE) → dist/quickanalyser"
go build -ldflags "$ldflags" -o dist/quickanalyser ./cmd/quickanalyser
echo "Built dist/quickanalyser ($QA_VERSION $MODE · $QA_COMMIT · dirty=$QA_DIRTY · $QA_BUILD_TIME)"
