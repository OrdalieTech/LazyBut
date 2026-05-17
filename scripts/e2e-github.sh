#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OWNER="${LAZYBUT_GH_OWNER:-netapy}"
REPO_NAME="${LAZYBUT_GH_REPO:-lazybut-e2e-$(date +%Y%m%d%H%M%S)-$RANDOM}"
FULL_REPO="$OWNER/$REPO_NAME"
WORK_ROOT="$(mktemp -d /private/tmp/lazybut-gh-e2e.XXXXXX)"
REPO="$WORK_ROOT/repo"
BIN="$WORK_ROOT/lazybut"
KEEP="${LAZYBUT_E2E_KEEP:-0}"
ALLOW_NO_DELETE_SCOPE="${LAZYBUT_GH_ALLOW_NO_DELETE_SCOPE:-0}"
CREATED=0

cleanup() {
  but -C "$REPO" teardown >/dev/null 2>&1 || true
  if [[ "$CREATED" == "1" && "$KEEP" != "1" ]]; then
    if gh repo delete "$FULL_REPO" --yes >/dev/null 2>&1; then
      printf 'deleted GitHub repo: %s\n' "$FULL_REPO"
    else
      printf 'failed to delete GitHub repo automatically: %s\n' "$FULL_REPO" >&2
      printf 'delete it manually when done: https://github.com/%s/settings\n' "$FULL_REPO" >&2
    fi
  fi
  if [[ "$KEEP" == "1" ]]; then
    printf 'keeping workdir: %s\n' "$WORK_ROOT"
    printf 'keeping GitHub repo: %s\n' "$FULL_REPO"
    return
  fi
  rm -rf "$WORK_ROOT"
}
trap cleanup EXIT

log() {
  printf '\n== %s ==\n' "$*"
}

run() {
  printf '+ %s\n' "$*"
  "$@"
}

run_retry() {
  local attempt status output
  output="$WORK_ROOT/retry.out"
  printf '+ %s\n' "$*"
  for attempt in 1 2 3 4 5 6; do
    if "$@" >"$output" 2>&1; then
      cat "$output"
      return 0
    fi
    status=$?
    if ! grep -Eiq 'database is locked|not mergeable|mergeable state|could not resolve to a pull request' "$output" || [[ "$attempt" == "6" ]]; then
      cat "$output" >&2
      return "$status"
    fi
    printf 'transient failure, retrying...\n' >&2
    sleep 2
  done
}

capture_retry() {
  local attempt status output
  output="$WORK_ROOT/capture.out"
  printf '+ %s\n' "$*" >&2
  for attempt in 1 2 3 4 5 6; do
    if "$@" >"$output" 2>&1; then
      cat "$output"
      return 0
    fi
    status=$?
    if ! grep -Eiq 'database is locked|not mergeable|mergeable state|could not resolve to a pull request' "$output" || [[ "$attempt" == "6" ]]; then
      cat "$output" >&2
      return "$status"
    fi
    printf 'transient failure, retrying...\n' >&2
    sleep 2
  done
}

require_delete_repo_scope() {
  local auth
  auth="$(gh auth status 2>&1)"
  printf '%s\n' "$auth"
  if ! grep -q "delete_repo" <<<"$auth"; then
    if [[ "$ALLOW_NO_DELETE_SCOPE" == "1" ]]; then
      printf '\nmissing GitHub scope: delete_repo\n' >&2
      printf 'Continuing because LAZYBUT_GH_ALLOW_NO_DELETE_SCOPE=1; manual cleanup may be required for %s.\n' "$FULL_REPO" >&2
      return
    fi
    printf '\nmissing GitHub scope: delete_repo\n' >&2
    printf 'Refusing to create %s because cleanup would not be guaranteed.\n' "$FULL_REPO" >&2
    printf 'Run `gh auth refresh -h github.com -s delete_repo` yourself, then retry.\n' >&2
    exit 2
  fi
}

log "preflight GitHub auth"
require_delete_repo_scope

log "build lazybut"
run env GOCACHE=/private/tmp/lazybutler-gocache go -C "$ROOT" build -o "$BIN" ./cmd/lazybut

log "create temporary GitHub repo"
run gh repo create "$FULL_REPO" --private --disable-issues --disable-wiki
CREATED=1

log "create base branch"
run git init "$REPO"
run git -C "$REPO" config user.name "Lazybut GitHub E2E"
run git -C "$REPO" config user.email "lazybut-github-e2e@example.test"
run git -C "$REPO" remote add origin "https://github.com/$FULL_REPO.git"
printf 'base\n' >"$REPO/README.md"
run git -C "$REPO" add README.md
run git -C "$REPO" commit -m "base"
run git -C "$REPO" branch -M main
run git -C "$REPO" push -u origin main

log "setup GitButler and render lazybut"
run_retry but -C "$REPO" setup --init
run_retry but -C "$REPO" status -j >/dev/null
run "$BIN" -C "$REPO" -snapshot 140x36 >/dev/null
run "$BIN" -C "$REPO" -snapshot 96x28 >/dev/null
run "$BIN" -C "$REPO" -snapshot 60x20 >/dev/null

log "commit and push GitButler branch to GitHub"
run_retry but -C "$REPO" branch new e2e-alpha
printf 'alpha\n' >"$REPO/alpha.txt"
run_retry but -C "$REPO" stage alpha.txt e2e-alpha --status-after -j >/dev/null
run_retry but -C "$REPO" commit e2e-alpha -m "add alpha" --status-after -j >/dev/null
run_retry but -C "$REPO" push e2e-alpha

log "create and update GitHub PR through GitButler"
run_retry but -C "$REPO" pr new e2e-alpha --default
PR_NUMBER="$(capture_retry gh pr view e2e-alpha --repo "$FULL_REPO" --json number -q .number)"
run_retry but -C "$REPO" pr set-draft e2e-alpha --status-after -j >/dev/null
run_retry but -C "$REPO" pr set-ready e2e-alpha --status-after -j >/dev/null

log "merge PR on GitHub and pull through GitButler"
run_retry gh pr merge "$PR_NUMBER" --repo "$FULL_REPO" --merge --delete-branch
run_retry but -C "$REPO" pull --check
run_retry but -C "$REPO" pull --status-after -j >/dev/null
run "$BIN" -C "$REPO" -snapshot 120x30 >/dev/null

log "done"
printf 'tested GitHub repo: %s\n' "$FULL_REPO"
printf 'tested workdir: %s\n' "$WORK_ROOT"
