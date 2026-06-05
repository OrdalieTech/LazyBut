#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_BASE="${TMPDIR:-/tmp}"
TMP_BASE="${TMP_BASE%/}"
WORK_ROOT="$(mktemp -d "$TMP_BASE/lazybut-e2e.XXXXXX")"
REMOTE="$WORK_ROOT/remote.git"
REPO="$WORK_ROOT/repo"
CLONE="$WORK_ROOT/clone"
MERGE_REPO="$WORK_ROOT/merge-repo"
BIN="$WORK_ROOT/lazybut"
CLEANUP="${LAZYBUT_E2E_KEEP:-0}"

cleanup() {
  if [[ "$CLEANUP" == "1" ]]; then
    printf 'keeping workdir: %s\n' "$WORK_ROOT"
    return
  fi
  but -C "$REPO" teardown >/dev/null 2>&1 || true
  but -C "$MERGE_REPO" teardown >/dev/null 2>&1 || true
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
  for attempt in 1 2 3 4 5; do
    if "$@" >"$output" 2>&1; then
      cat "$output"
      return 0
    fi
    status=$?
    if ! grep -qi 'database is locked' "$output" || [[ "$attempt" == "5" ]]; then
      cat "$output" >&2
      return "$status"
    fi
    printf 'database locked, retrying...\n' >&2
    sleep 0.5
  done
}

status_json() {
  run_retry but -C "$REPO" status -j >/dev/null
}

log "build lazybut"
run env GOCACHE="$TMP_BASE/lazybutler-gocache" go -C "$ROOT" build -o "$BIN" ./cmd/lazybut

log "create empty git repo with bare remote"
run git init --bare "$REMOTE"
run git init "$REPO"
run git -C "$REPO" config user.name "Lazybut E2E"
run git -C "$REPO" config user.email "lazybut-e2e@example.test"
run git -C "$REPO" remote add origin "$REMOTE"
printf 'base\n' >"$REPO/README.md"
run git -C "$REPO" add README.md
run git -C "$REPO" commit -m "base"
run git -C "$REPO" branch -M main
run git -C "$REPO" push -u origin main
run git --git-dir="$REMOTE" symbolic-ref HEAD refs/heads/main

log "setup GitButler"
run_retry but -C "$REPO" setup --init
status_json
run "$BIN" -C "$REPO" -snapshot 140x36 >/dev/null
run "$BIN" -C "$REPO" -snapshot 96x28 >/dev/null
run "$BIN" -C "$REPO" -snapshot 60x20 >/dev/null

log "branch create, stage, commit"
run_retry but -C "$REPO" branch new e2e-alpha
printf 'alpha one\n' >"$REPO/alpha.txt"
run_retry but -C "$REPO" status -j
run_retry but -C "$REPO" stage alpha.txt e2e-alpha --status-after -j >/dev/null
run_retry but -C "$REPO" commit e2e-alpha -m "add alpha" --status-after -j >/dev/null
status_json

log "partial commit with selected file ids shape"
printf 'beta one\n' >"$REPO/beta.txt"
run_retry but -C "$REPO" stage beta.txt e2e-alpha --status-after -j >/dev/null
run_retry but -C "$REPO" commit e2e-alpha --only -m "add beta" --status-after -j >/dev/null
status_json

log "stacked branch and history edits"
run_retry but -C "$REPO" branch new --anchor e2e-alpha e2e-beta
printf 'stacked\n' >"$REPO/stacked.txt"
run_retry but -C "$REPO" stage stacked.txt e2e-beta --status-after -j >/dev/null
run_retry but -C "$REPO" commit e2e-beta -m "add stacked" --status-after -j >/dev/null
run_retry but -C "$REPO" reword e2e-beta -m "e2e-beta-renamed" --status-after -j >/dev/null
run_retry but -C "$REPO" branch show e2e-beta-renamed --files -j >/dev/null
status_json

log "push dry-run and push"
run_retry but -C "$REPO" push e2e-alpha --dry-run
run_retry but -C "$REPO" push e2e-alpha
run_retry but -C "$REPO" push e2e-beta-renamed
status_json

log "pull check and second clone remote update"
run git clone "$REMOTE" "$CLONE"
run git -C "$CLONE" config user.name "Lazybut E2E Remote"
run git -C "$CLONE" config user.email "lazybut-e2e-remote@example.test"
printf 'remote change\n' >>"$CLONE/README.md"
run git -C "$CLONE" add README.md
run git -C "$CLONE" commit -m "remote update"
run git -C "$CLONE" push origin main
run_retry but -C "$REPO" pull --check
run_retry but -C "$REPO" pull --status-after -j >/dev/null
status_json

log "undo, oplog, clean"
run_retry but -C "$REPO" undo --status-after -j >/dev/null
run_retry but -C "$REPO" oplog snapshot -m "lazybut e2e snapshot"
run_retry but -C "$REPO" clean --dry-run
run_retry but -C "$REPO" clean --status-after -j >/dev/null
status_json

log "merge with gb-local target"
run git init "$MERGE_REPO"
run git -C "$MERGE_REPO" config user.name "Lazybut E2E"
run git -C "$MERGE_REPO" config user.email "lazybut-e2e@example.test"
printf 'base\n' >"$MERGE_REPO/README.md"
run git -C "$MERGE_REPO" add README.md
run git -C "$MERGE_REPO" commit -m "base"
run git -C "$MERGE_REPO" branch -M main
run_retry but -C "$MERGE_REPO" setup --init
run_retry but -C "$MERGE_REPO" branch new e2e-merge
printf 'merge me\n' >"$MERGE_REPO/merge.txt"
run_retry but -C "$MERGE_REPO" stage merge.txt e2e-merge --status-after -j >/dev/null
run_retry but -C "$MERGE_REPO" commit e2e-merge -m "merge branch" --status-after -j >/dev/null
run_retry but -C "$MERGE_REPO" merge e2e-merge --status-after -j >/dev/null
run_retry but -C "$MERGE_REPO" status -j >/dev/null

log "error surfaces"
if "$BIN" --but-bin "$TMP_BASE/missing-but" -C "$REPO" -snapshot 80x20 | grep -q "install GitButler CLI"; then
  printf '+ missing but error surfaced\n'
else
  printf 'missing but error was not surfaced\n' >&2
  exit 1
fi

log "done"
printf 'tested workdir: %s\n' "$WORK_ROOT"
