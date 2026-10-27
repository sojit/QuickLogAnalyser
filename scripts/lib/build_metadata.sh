#!/usr/bin/env bash
# Shared QuickAnalyser build metadata (POSIX). Source from build/verify scripts.
# Release mode must validate Git + VERSION match BEFORE any synchronization.

qa_repo_root() {
  local here
  here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  printf '%s' "$here"
}

qa_version_label() {
  local root="$1"
  tr -d '\r\n\357\273\277' <"$root/VERSION"
}

qa_embedded_version() {
  local root="$1"
  tr -d '\r\n\357\273\277' <"$root/internal/appinfo/version.txt"
}

qa_sync_version_embed() {
  local root="$1" version="$2"
  printf '%s' "$version" >"$root/internal/appinfo/version.txt"
}

# Sets: QA_COMMIT QA_COMMIT_FULL QA_DIRTY QA_BUILD_TIME QA_GIT_AVAILABLE QA_HAS_HEAD QA_DIRTY_REASON
# Optional second arg: reuse BuildTime
qa_build_metadata() {
  local root="$1"
  if [[ -n "${2:-}" ]]; then
    QA_BUILD_TIME="$2"
  else
    QA_BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  fi
  QA_COMMIT=unknown
  QA_COMMIT_FULL=unknown
  QA_DIRTY=true
  QA_GIT_AVAILABLE=false
  QA_HAS_HEAD=false
  QA_DIRTY_REASON=git-unavailable

  if ! command -v git >/dev/null 2>&1; then
    return 0
  fi
  if ! git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    QA_DIRTY_REASON=not-a-git-worktree
    return 0
  fi
  QA_GIT_AVAILABLE=true

  if ! QA_COMMIT_FULL="$(git -C "$root" rev-parse HEAD 2>/dev/null)"; then
    QA_COMMIT_FULL=unknown
    QA_DIRTY_REASON=unborn-or-missing-HEAD
    return 0
  fi
  QA_HAS_HEAD=true
  QA_COMMIT="$(git -C "$root" rev-parse --short HEAD 2>/dev/null || printf '%s' "${QA_COMMIT_FULL:0:12}")"

  if ! status_out="$(git -C "$root" status --porcelain --untracked-files=normal 2>/dev/null)"; then
    QA_DIRTY_REASON=status-failed
    return 0
  fi
  if [[ -n "$(printf '%s' "$status_out" | tr -d '[:space:]')" ]]; then
    QA_DIRTY=true
    QA_DIRTY_REASON=working-tree-dirty
  else
    QA_DIRTY=false
    QA_DIRTY_REASON=clean
  fi
}

qa_assert_release_metadata() {
  local root="$1" version="$2"
  local errs=()
  if [[ "$QA_GIT_AVAILABLE" != true ]]; then
    errs+=("release build requires Git and a Git worktree")
  fi
  if [[ "$QA_HAS_HEAD" != true || "$QA_COMMIT" == unknown || "$QA_COMMIT_FULL" == unknown ]]; then
    errs+=("release build rejects unknown commit / unborn repository")
  fi
  if [[ "$QA_DIRTY" != false ]]; then
    errs+=("release build requires a clean working tree (reason: $QA_DIRTY_REASON)")
  fi
  local embedded
  if [[ ! -f "$root/internal/appinfo/version.txt" ]]; then
    errs+=("internal/appinfo/version.txt missing before release preflight")
  else
    embedded="$(qa_embedded_version "$root")"
    if [[ "$embedded" != "$version" ]]; then
      errs+=("embedded version.txt '$embedded' does not match VERSION '$version'")
    fi
  fi
  if ((${#errs[@]} > 0)); then
    printf 'Release build refused:\n' >&2
    printf ' - %s\n' "${errs[@]}" >&2
    return 1
  fi
}

# Release: assert then never sync. Development: sync after metadata read.
qa_prepare_identity() {
  local root="$1" mode="$2"
  QA_VERSION="$(qa_version_label "$root")"
  qa_build_metadata "$root"
  if [[ "$mode" == release ]]; then
    qa_assert_release_metadata "$root" "$QA_VERSION"
  else
    qa_sync_version_embed "$root" "$QA_VERSION"
    local bt="$QA_BUILD_TIME"
    qa_build_metadata "$root" "$bt"
  fi
  QA_MODE="$mode"
  QA_STATUS=development_unreleased
  QA_OS=linux
  QA_ARCH=amd64
}

qa_ldflags() {
  printf -- '-X quickanalyser/internal/appinfo.Commit=%s -X quickanalyser/internal/appinfo.Dirty=%s -X quickanalyser/internal/appinfo.BuildTime=%s' \
    "$QA_COMMIT" "$QA_DIRTY" "$QA_BUILD_TIME"
}

qa_write_identity_json() {
  local path="$1"
  mkdir -p "$(dirname "$path")"
  cat >"$path" <<EOF
{
  "product": "QuickAnalyser",
  "signature": "by Soji",
  "version": "$QA_VERSION",
  "commit": "$QA_COMMIT",
  "commit_full": "$QA_COMMIT_FULL",
  "dirty": "$QA_DIRTY",
  "build_time": "$QA_BUILD_TIME",
  "mode": "$QA_MODE",
  "status": "$QA_STATUS",
  "os": "$QA_OS",
  "arch": "$QA_ARCH",
  "copyright": "Copyright (c) 2026 Soji",
  "license": "MIT"
}
EOF
}
