#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_BASE="${TMPDIR:-/tmp}"
TMP_BASE="${TMP_BASE%/}"
WORK_ROOT="$(mktemp -d "$TMP_BASE/lazybut-e2e.XXXXXX")"
REMOTE="$WORK_ROOT/remote.git"
REPO="$WORK_ROOT/repo"
CLONE="$WORK_ROOT/clone"
LAND_REPO="$WORK_ROOT/land-repo"
BIN="$WORK_ROOT/lazybut"
CLEANUP="${LAZYBUT_E2E_KEEP:-0}"

cleanup() {
  if [[ "$CLEANUP" == "1" ]]; then
    printf 'keeping workdir: %s\n' "$WORK_ROOT"
    return
  fi
  but -C "$REPO" teardown >/dev/null 2>&1 || true
  but -C "$LAND_REPO" teardown >/dev/null 2>&1 || true
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
    else
      status=$?
    fi
    if ! grep -qi 'database is locked' "$output" || [[ "$attempt" == "5" ]]; then
      cat "$output" >&2
      return "$status"
    fi
    printf 'database locked, retrying...\n' >&2
    sleep 0.5
  done
}

status_json() {
  run_retry but -C "$REPO" status --json >/dev/null
}

change_id() {
  local repo="$1" path="$2"
  but -C "$repo" status --json | jq -er --arg path "$path" '(.uncommittedChanges // .unassignedChanges // [])[] | select(.filePath == $path) | .cliId'
}

branch_commit_ids() {
  local repo="$1" branch="$2"
  but -C "$repo" status --json | jq -er --arg branch "$branch" '.stacks[].branches[] | select(.name == $branch) | .commits[].cliId'
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

log "branch create, unapply, and apply"
run_retry but -C "$REPO" branch new e2e-empty --json --status-after >/dev/null
run_retry but -C "$REPO" unapply e2e-empty --json --status-after >/dev/null
run_retry but -C "$REPO" apply e2e-empty --json --status-after >/dev/null

log "branch create and selected commit"
printf 'alpha one\n' >"$REPO/alpha.txt"
ALPHA_ID="$(change_id "$REPO" alpha.txt)"
run_retry but -C "$REPO" commit -b e2e-alpha -m "add alpha" "$ALPHA_ID" --json --status-after >/dev/null
status_json

log "second selected commit"
printf 'beta one\n' >"$REPO/beta.txt"
BETA_ID="$(change_id "$REPO" beta.txt)"
run_retry but -C "$REPO" commit -b e2e-alpha -m "add beta" "$BETA_ID" --json --status-after >/dev/null
status_json

log "amend, squash, uncommit, and recommit"
printf 'amended\n' >"$REPO/amended.txt"
AMEND_ID="$(change_id "$REPO" amended.txt)"
run_retry but -C "$REPO" amend -t e2e-alpha "$AMEND_ID" --json --status-after >/dev/null
mapfile -t ALPHA_COMMITS < <(branch_commit_ids "$REPO" e2e-alpha)
[[ "${#ALPHA_COMMITS[@]}" == "2" ]]
run_retry but -C "$REPO" squash "${ALPHA_COMMITS[0]}" -t "${ALPHA_COMMITS[1]}" --use-target-message --json --status-after >/dev/null
mapfile -t ALPHA_COMMITS < <(branch_commit_ids "$REPO" e2e-alpha)
[[ "${#ALPHA_COMMITS[@]}" == "1" ]]
run_retry but -C "$REPO" uncommit "${ALPHA_COMMITS[0]}" --json --status-after >/dev/null
mapfile -t RECOMMIT_IDS < <(but -C "$REPO" status --json | jq -er '(.uncommittedChanges // .unassignedChanges // [])[].cliId')
[[ "${#RECOMMIT_IDS[@]}" == "3" ]]
run_retry but -C "$REPO" commit -b e2e-alpha -m "recommit alpha" "${RECOMMIT_IDS[@]}" --json --status-after >/dev/null

log "discard change and branch"
printf 'discard me\n' >"$REPO/discard.txt"
DISCARD_ID="$(change_id "$REPO" discard.txt)"
run_retry but -C "$REPO" discard "$DISCARD_ID" --json --status-after >/dev/null
[[ ! -e "$REPO/discard.txt" ]]
printf 'delete me\n' >"$REPO/delete.txt"
DELETE_ID="$(change_id "$REPO" delete.txt)"
run_retry but -C "$REPO" commit -b e2e-delete -m "delete branch" "$DELETE_ID" --json --status-after >/dev/null
run_retry but -C "$REPO" discard e2e-delete --json --status-after >/dev/null
! but -C "$REPO" status --json | jq -e '.stacks[].branches[] | select(.name == "e2e-delete")' >/dev/null

log "stacked branch and history edits"
printf 'stacked\n' >"$REPO/stacked.txt"
STACKED_ID="$(change_id "$REPO" stacked.txt)"
run_retry but -C "$REPO" commit -b e2e-beta -m "add stacked" "$STACKED_ID" --json --status-after >/dev/null
run_retry but -C "$REPO" move e2e-beta --above e2e-alpha --json --status-after >/dev/null
run_retry but -C "$REPO" move e2e-beta --unstack --json --status-after >/dev/null
run_retry but -C "$REPO" move e2e-beta --above e2e-alpha --json --status-after >/dev/null
run_retry but -C "$REPO" reword e2e-beta -m "e2e-beta-renamed" --json --status-after >/dev/null
run_retry but -C "$REPO" branch show e2e-beta-renamed --files --json >/dev/null
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
run_retry but -C "$REPO" pull --json --status-after >/dev/null
status_json

log "undo, oplog, clean"
run_retry but -C "$REPO" undo --json --status-after >/dev/null
run_retry but -C "$REPO" oplog snapshot -m "lazybut e2e snapshot"
run_retry but -C "$REPO" clean --dry-run
run_retry but -C "$REPO" clean --json --status-after >/dev/null
status_json

log "land with local target"
run git init "$LAND_REPO"
run git -C "$LAND_REPO" config user.name "Lazybut E2E"
run git -C "$LAND_REPO" config user.email "lazybut-e2e@example.test"
printf 'base\n' >"$LAND_REPO/README.md"
run git -C "$LAND_REPO" add README.md
run git -C "$LAND_REPO" commit -m "base"
run git -C "$LAND_REPO" branch -M main
run_retry but -C "$LAND_REPO" setup --init
printf 'land me\n' >"$LAND_REPO/land.txt"
LAND_ID="$(change_id "$LAND_REPO" land.txt)"
run_retry but -C "$LAND_REPO" commit -b e2e-land -m "land branch" "$LAND_ID" --json --status-after >/dev/null
run_retry but -C "$LAND_REPO" land e2e-land --yes --json --status-after >/dev/null
run_retry but -C "$LAND_REPO" status --json >/dev/null
git -C "$LAND_REPO" show gb-local/main:land.txt | grep -qx 'land me'

log "error surfaces"
if "$BIN" --but-bin "$TMP_BASE/missing-but" -C "$REPO" -snapshot 80x20 | grep -q "install GitButler CLI"; then
  printf '+ missing but error surfaced\n'
else
  printf 'missing but error was not surfaced\n' >&2
  exit 1
fi

log "done"
printf 'tested workdir: %s\n' "$WORK_ROOT"
