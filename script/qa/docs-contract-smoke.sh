#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' 'usage: script/qa/docs-contract-smoke.sh --uam <binary> --evidence-dir <absolute-dir>' >&2
  exit 2
}

uam=''
evidence_dir=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --uam) uam=${2:-}; shift 2 ;;
    --evidence-dir) evidence_dir=${2:-}; shift 2 ;;
    *) usage ;;
  esac
done

[ -n "$uam" ] && [ -x "$uam" ] || { printf '%s\n' 'docs smoke: --uam must name an executable' >&2; exit 2; }
[ -n "$evidence_dir" ] && [ "${evidence_dir#/}" != "$evidence_dir" ] || { printf '%s\n' 'docs smoke: --evidence-dir must be absolute' >&2; exit 2; }
uam=$(cd "$(dirname "$uam")" && printf '%s/%s' "$(pwd -P)" "$(basename "$uam")")
mkdir -p "$evidence_dir"
evidence_dir=$(cd "$evidence_dir" && pwd -P)

repo_root=$(cd "$(dirname "$0")/../.." && pwd -P)
cd "$repo_root"
real_config=/home/dev/.config/uam
scratch=$(mktemp -d)
config_dir="$scratch/config"
session_dir="$scratch/session"
xdg_config_home="$scratch/xdg-config"
cache_dir="$scratch/cache"
transcript="$evidence_dir/command-transcript.txt"
help_file="$evidence_dir/help.txt"
link_report="$evidence_dir/link-report.txt"
reader_status="$evidence_dir/reader-executable-status.tsv"
terminal_smoke="$evidence_dir/terminal-smoke-real.json"

case "$config_dir" in
  /home/dev/.config/uam|/home/dev/.config/uam/*)
    printf '%s\n' 'docs smoke: refusing real UAM config path' >&2
    exit 2
    ;;
esac

fingerprint() {
  if [ -e "$real_config/sessions.json" ]; then
    sha256sum "$real_config/sessions.json" | awk '{print $1}'
  else
    printf '%s' absent
  fi
}

real_before=$(fingerprint)
mkdir -p "$config_dir" "$session_dir" "$xdg_config_home" "$cache_dir"
export UAM_CONFIG_DIR="$config_dir"
export UAM_SESSION_DIR="$session_dir"
export XDG_CONFIG_HOME="$xdg_config_home"
export UAM_CACHE_DIR="$cache_dir"

cleanup() {
  status=$?
  real_after=$(fingerprint)
  removed=false
  rm -rf "$scratch"
  [ ! -e "$scratch" ] && removed=true
  printf '{\n  "isolated_config": %s,\n  "real_config_path": "%s",\n  "real_config_unchanged": %s,\n  "scratch_removed": %s,\n  "exit_status": %s\n}\n' \
    'true' "$real_config" "$( [ "$real_before" = "$real_after" ] && printf true || printf false )" "$removed" "$status" \
    > "$evidence_dir/cleanup-receipt.json"
  [ "$real_before" = "$real_after" ] || { printf '%s\n' 'docs smoke: real UAM config changed' >&2; exit 1; }
}
trap cleanup EXIT

: > "$transcript"
run() {
  label=$1
  shift
  {
    printf '$'
    printf ' %q' "$@"
    printf '\n'
    "$@"
    printf '[exit 0]\n\n'
  } >> "$transcript" 2>&1
}

run_expect_fail() {
  label=$1
  shift
  {
    printf '$'
    printf ' %q' "$@"
    printf '\n'
  } >> "$transcript"
  if "$@" >> "$transcript" 2>&1; then
    printf '%s\n' "docs smoke: expected failure succeeded: $label" >&2
    exit 1
  fi
  printf '[expected nonzero]\n\n' >> "$transcript"
}

run_reader() {
  case_id=$1
  shift
  {
    printf '$'
    printf ' %q' "$@"
    printf '\n'
  } >> "$transcript"
  if "$@" >> "$transcript" 2>&1; then
    printf '[exit 0]\n\n' >> "$transcript"
    printf '%s\t0\n' "$case_id" >> "$reader_status"
    return 0
  fi
  status=$?
  printf '[exit %s]\n\n' "$status" >> "$transcript"
  printf '%s\t%s\n' "$case_id" "$status" >> "$reader_status"
  return "$status"
}

run 'help' "$uam" --help
cp "$transcript" "$help_file"
grep -F 'uam profile effective <session-id> [--json]' "$help_file" >/dev/null
grep -F 'uam doctor [<session-id>] [--json]' "$help_file" >/dev/null

run 'profile set focused' "$uam" profile set focused --provider claude --mode safe --mouse off --prefix C-a --back-detach off --scrollback 8000
run 'profile default focused' "$uam" profile default focused
run 'profile show focused json' "$uam" profile show focused --json
run 'profile ls json' "$uam" profile ls --json
run 'doctor global json' "$uam" doctor --json
run 'profile default none' "$uam" profile default none
run 'profile remove focused' "$uam" profile rm focused

cp "$repo_root/internal/store/testdata/schema-v3-full.json" "$config_dir/sessions.json"
run 'trigger schema migration' "$uam" profile set migrated --provider claude --mode safe --mouse off --prefix C-a --back-detach off --scrollback 8000
backup=$(find "$config_dir" -maxdepth 1 -type f -name 'sessions.json.bak.*' -print -quit)
[ -n "$backup" ] && cmp "$repo_root/internal/store/testdata/schema-v3-full.json" "$backup"
grep -Eq '"schema_version"[[:space:]]*:[[:space:]]*4' "$config_dir/sessions.json"
grep -F '"top_level_extension"' "$config_dir/sessions.json" >/dev/null
grep -F '"session_extension"' "$config_dir/sessions.json" >/dev/null

session_id=12345678-1234-4234-9234-123456789abc
run 'profile assign' "$uam" profile assign "$session_id" migrated
run 'profile override' "$uam" profile override "$session_id" --mouse on
run 'profile effective json' "$uam" profile effective "$session_id" --json
run 'doctor session json' "$uam" doctor "$session_id" --json
run_expect_fail 'referenced profile delete' "$uam" profile rm migrated
run 'profile assign none' "$uam" profile assign "$session_id" none
run 'profile remove migrated' "$uam" profile rm migrated
run 'cross terminal collector' "$repo_root/scripts/terminal-smoke-real" --terminal kitty --output "$terminal_smoke" --non-interactive
grep -F '"terminal": "kitty"' "$terminal_smoke" >/dev/null

: > "$link_report"
link_fail=0
while IFS= read -r source; do
  while IFS= read -r target; do
    case "$target" in
      ''|http://*|https://*|mailto:*|\#*) continue ;;
    esac
    path=${target%%#*}
    case "$path" in
      /*) resolved="$repo_root$path" ;;
      *) resolved="$(dirname "$source")/$path" ;;
    esac
    if [ -e "$resolved" ]; then
      printf 'PASS %s -> %s\n' "$source" "$target" >> "$link_report"
    else
      printf 'FAIL %s -> %s\n' "$source" "$target" >> "$link_report"
      link_fail=1
    fi
  done < <(sed -nE 's/.*\]\(([^)#]+).*/\1/p' "$source")
done < <(find "$repo_root/docs" -type f -name '*.md' -print; printf '%s\n' "$repo_root/README.md")
[ "$link_fail" -eq 0 ]

: > "$reader_status"
run_reader two_clients go test ./internal/session -run '^(TestControllerAssignmentSingleWriter|TestAttachProtocolCompatibilityMatrix)$' -count=1
run_reader deleted_profile go test ./internal/cli -run '^TestReferencedProfileDeleteRejected$' -count=1
run_reader old_host_and_unsupported_term go test ./internal/session -run '^(TestAttachProtocolCompatibilityMatrix|TestTodo11UnsupportedTermHintIsRedacted)$' -count=1
run_reader migration_and_runtime_persistence go test ./internal/store -run '^(TestMigrateV3ProfilesPreservesSessions|TestRuntimeClientStateIsNotPersisted)$' -count=1
awk -F '\t' '
BEGIN {
  print "{"
  print "  \"executable_cases\": ["
}
{
  if (NR > 1) print ","
  printf "    {\"id\":\"%s\",\"exit_status\":%s,\"evidence\":\"command-transcript.txt\"}", $1, $2
}
END {
  print ""
  print "  ],"
  print "  \"semantic_cases\": ["
  print "    {\"id\":\"reboot\",\"status\":\"independent_review_required\",\"source\":\"F1/F3 reader review\",\"evidence\":\"README resuming sessions; ADR 0003 persistence and recovery\"},"
  print "    {\"id\":\"sigkill\",\"status\":\"independent_review_required\",\"source\":\"F1/F3 reader review\",\"evidence\":\"ADR 0002 consequences; ADR 0003 persistence and recovery\"}"
  print "  ]"
  print "}"
}' "$reader_status" > "$evidence_dir/reader-checklist.json"
printf '%s\n' '{' \
  '  "purpose": "Independent reader-review input; this file records questions and evidence, not verdicts.",' \
  '  "questions": [' \
  '    {"id":"two_clients","question":"Which attached client may write stdin, resize, or reply?","evidence":"command-transcript.txt; ADR 0003 roles"},' \
  '    {"id":"deleted_profile","question":"What happens when removing a profile selected by a session?","evidence":"command-transcript.txt; README profiles"},' \
  '    {"id":"old_host","question":"How does a v2 client behave with a v1 host?","evidence":"command-transcript.txt; ADR 0003 protocol matrix"},' \
  '    {"id":"unsupported_term","question":"Does a TERM hint prove capability or appear unredacted in diagnostics?","evidence":"command-transcript.txt; ADR 0002 and ADR 0003"},' \
  '    {"id":"reboot","question":"What survives, and what recovery action is available after reboot?","evidence":"README resuming sessions; ADR 0003 persistence and recovery"},' \
  '    {"id":"sigkill","question":"What cleanup guarantee remains after SIGKILL?","evidence":"ADR 0002 consequences; ADR 0003 persistence and recovery"}' \
  '  ]' \
  '}' > "$evidence_dir/reader-review-input.json"

printf '{\n  "current_schema": 4,\n  "migration_fixture": "schema-v3-full.json",\n  "migration_backup_exact": true,\n  "unknown_fields_preserved": true,\n  "help_profile_commands_present": true,\n  "help_doctor_command_present": true\n}\n' > "$evidence_dir/schema-cli-match.json"
