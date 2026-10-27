#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/build_metadata.sh
source "$ROOT/scripts/lib/build_metadata.sh"
cd "$ROOT"

MODE=development
if [[ "${1:-}" == "--release" ]]; then
  MODE=release
fi

go vet ./...
go test ./...
if command -v node >/dev/null 2>&1; then
  node "$ROOT/scripts/highlights_markup_test.cjs"
else
  echo "node not found — running Go Highlights structure test"
  go test ./web -count=1 -run TestHighlightsLayoutTwoIssuesOneFile
fi

qa_prepare_identity "$ROOT" "$MODE"
mkdir -p dist
qa_write_identity_json "$ROOT/dist/build-identity.json"
ldflags="$(qa_ldflags)"
echo "go build ($MODE) commit=$QA_COMMIT dirty=$QA_DIRTY build_time=$QA_BUILD_TIME"
go build -ldflags "$ldflags" -o dist/quickanalyser ./cmd/quickanalyser
echo "verify OK ($MODE · dirty=$QA_DIRTY · $QA_COMMIT · $QA_BUILD_TIME)"
