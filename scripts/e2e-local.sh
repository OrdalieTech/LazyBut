#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_BASE="${TMPDIR:-/tmp}"
TMP_BASE="${TMP_BASE%/}"
WORK_ROOT="$(mktemp -d "$TMP_BASE/lazybut-e2e.XXXXXX")"
REMOTE="$WORK_ROOT/remote.git"
REPO="$WORK_ROOT/repo"
BIN="$WORK_ROOT/lazybut"
CLEANUP="${LAZYBUT_E2E_KEEP:-0}"

cleanup() {
  if [[ "$CLEANUP" == "1" ]]; then
    printf 'keeping workdir: %s\n' "$WORK_ROOT"
    return
  fi
  but -C "$REPO" teardown >/dev/null 2>&1 || true
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

log "build lazybut ($(but --version))"
run env GOCACHE="$TMP_BASE/lazybutler-gocache" go -C "$ROOT" build -o "$BIN" ./cmd/lazybut

log "create git repo with bare remote"
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

log "setup GitButler and render snapshots"
# GitButler after 0.22 sets repositories up on first use and rejects `setup`;
# the status call that follows catches a real setup failure.
run but -C "$REPO" setup --init || true
run but -C "$REPO" branch new e2e-alpha --json >/dev/null
printf 'alpha\n' >"$REPO/alpha.txt"
run but -C "$REPO" status --json >/dev/null
run "$BIN" -C "$REPO" -snapshot 140x36 >/dev/null
run "$BIN" -C "$REPO" -snapshot 96x28 >/dev/null
run "$BIN" -C "$REPO" -snapshot 60x20 >/dev/null

log "every client mutation against the installed but"
run env LAZYBUT_E2E=1 go -C "$ROOT" test ./internal/gitbutler -run TestClientAgainstRealGitButler -count=1

log "error surfaces"
if "$BIN" --but-bin "$TMP_BASE/missing-but" -C "$REPO" -snapshot 80x20 | grep -q "install GitButler CLI"; then
  printf '+ missing but error surfaced\n'
else
  printf 'missing but error was not surfaced\n' >&2
  exit 1
fi

log "done"
