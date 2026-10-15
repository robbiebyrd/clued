#!/usr/bin/env bash
# Checks that clued's SessionStart prime report reaches the model, once, in
# each way clued's hooks can be registered. Manual: it runs real `claude -p`
# sessions (a few cents each), so CI, which has no Claude credentials, never
# runs it; run it on each release and record the table in the story's work
# log.
#
#   scripts/verify-prime.sh [--claude <bin>] [--repo <dir>] [--keep]
#
#   --claude <bin>   the Claude Code binary (default: claude on PATH)
#   --repo <dir>     the project the sessions start in (default: a new empty
#                    git repository)
#   --keep           keep the work directory (config dirs, replies, debug logs)
#
# Configurations, each in a fresh CLAUDE_CONFIG_DIR holding only the
# credentials file:
#   A  plugin      the plugin installed from a local marketplace of this
#                  checkout (hooks/hooks.json only; settings.json has no
#                  clued hook)
#   B  settings    the plugin not installed; `hooks/register-hooks
#                  --settings` mirrors every hook into settings.json through
#                  the clued-hook launcher
#   C  upgrade     the plugin installed and settings.json still holding the
#                  mirror an older clued wrote (absolute plugin-root paths),
#                  then the installed `hooks/register-hooks` run as
#                  session-start runs it: the mirror is gone, so prime runs
#                  once
#   D  resume      A, then `claude -p --resume <session>`
#
# Prints one row per configuration:
#   claude --version | config | hook ran | context seen | copies
# where "hook ran" counts prime executions in the --debug hooks log,
# "context seen" is whether the model saw a block starting "clued prime
# report for", and "copies" how many such blocks the run itself added (for
# D, the blocks of the resume). It also checks that every SessionStart
# command path in the settings and in the installed hooks/hooks.json exists.
# Exits 0 when every configuration shows exactly one copy, 1 when one does
# not, 2 on a usage error and 3 when there are no Claude credentials.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLAUDE_BIN=claude
REPO=""
KEEP=0
while (( $# > 0 )); do
  case "$1" in
    --claude) [[ -n "${2:-}" ]] || { echo "usage: $0 [--claude <bin>] [--repo <dir>] [--keep]" >&2; exit 2; }; CLAUDE_BIN="$2"; shift 2 ;;
    --repo) [[ -n "${2:-}" ]] || { echo "usage: $0 [--claude <bin>] [--repo <dir>] [--keep]" >&2; exit 2; }; REPO="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,38p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "usage: $0 [--claude <bin>] [--repo <dir>] [--keep]" >&2; exit 2 ;;
  esac
done

command -v "$CLAUDE_BIN" >/dev/null || { echo "verify-prime: $CLAUDE_BIN not found" >&2; exit 2; }
command -v jq >/dev/null || { echo "verify-prime: jq not found" >&2; exit 2; }

# The credentials: the file of the current config dir (Linux, or a copied
# one), else a token or key in the environment, which the child inherits.
# On macOS the login lives in the keychain under the config dir, so a fresh
# config dir needs CLAUDE_CODE_OAUTH_TOKEN (`claude setup-token`) instead.
SRC_CFG="${CLAUDE_CONFIG_DIR:-${HOME}/.claude}"
CREDS=""
[[ -f "${SRC_CFG}/.credentials.json" ]] && CREDS="${SRC_CFG}/.credentials.json"
if [[ -z "$CREDS" && -z "${ANTHROPIC_API_KEY:-}" && -z "${ANTHROPIC_AUTH_TOKEN:-}" \
  && -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" && -z "${ANTHROPIC_BASE_URL:-}" ]]; then
  echo "verify-prime: no Claude credentials (no ${SRC_CFG}/.credentials.json and none of ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_BASE_URL); run \`claude setup-token\` and export CLAUDE_CODE_OAUTH_TOKEN" >&2
  exit 3
fi

# A run started from inside a Claude Code session must be a session of its
# own: these tie a child to its parent's session, transcript and sockets.
for v in CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_REMOTE_SESSION_ID CLAUDE_CODE_CHILD_SESSION \
  CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_SESSION_ATTENDED CLAUDE_CODE_WORKER_EPOCH CLAUDE_PID \
  SESSION_INGRESS_URL CLAUDE_SESSION_INGRESS_TOKEN_FILE CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2 \
  CLAUDE_CODE_MESSAGING_SOCKET CLAUDE_CODE_MESSAGING_TOKEN CLAUDE_CODE_SYNC_SESSION_REFS \
  CLAUDE_CODE_TEE_SDK_STDOUT CLAUDE_CODE_DIAGNOSTICS_FILE CLAUDE_CODE_REMOTE_SDK_URL \
  CLAUDE_CODE_SYNC_PLUGINS CLAUDE_CODE_SYNC_SKILLS CLAUDE_PLUGIN_ROOT CLAUDE_PLUGIN_DATA; do
  unset "$v"
done

WORK="$(mktemp -d "${TMPDIR:-/tmp}/verify-prime.XXXXXX")"
if (( KEEP )); then
  echo "verify-prime: work directory ${WORK}" >&2
else
  trap 'rm -rf "$WORK"' EXIT
fi
if [[ -z "$REPO" ]]; then
  REPO="${WORK}/repo"
  mkdir -p "$REPO" && git -C "$REPO" init -q
fi
REPO="$(cd "$REPO" && pwd)"

# A local marketplace whose clued is this checkout as it is on disk
# (uncommitted changes included), so the installed plugin is the current
# build rather than the GitHub source the published marketplace names.
MARKET="${WORK}/market"
mkdir -p "${MARKET}/.claude-plugin" "${MARKET}/clued"
(cd "$ROOT" && git ls-files -co --exclude-standard -z | xargs -0 tar -cf -) | tar -C "${MARKET}/clued" -xf -
jq '.plugins[0].source = "./clued"' "${ROOT}/.claude-plugin/marketplace.json" > "${MARKET}/.claude-plugin/marketplace.json"

PROMPT='Look for blocks in your context whose first line starts with "clued prime report for". Reply with the first line of each such block verbatim, one per line, in the order they appear, and then a last line with only the number of such blocks. If there is none, reply NONE.'
VERSION="$("$CLAUDE_BIN" --version 2>/dev/null | head -n 1)"

# new_config NAME prints a fresh config dir holding only the credentials.
new_config() {
  local cfg="${WORK}/${1}/config"
  mkdir -p "$cfg"
  [[ -n "$CREDS" ]] && cp "$CREDS" "${cfg}/.credentials.json"
  echo "$cfg"
}

# install_plugin CFG installs clued@clued from the local marketplace.
install_plugin() {
  CLAUDE_CONFIG_DIR="$1" "$CLAUDE_BIN" plugin marketplace add "$MARKET" >/dev/null 2>&1 \
    && CLAUDE_CONFIG_DIR="$1" "$CLAUDE_BIN" plugin install clued@clued >/dev/null 2>&1
}

# installed_root CFG prints the installed plugin's root.
installed_root() {
  jq -r '.plugins["clued@clued"][0].installPath // empty' "${1}/plugins/installed_plugins.json" 2>/dev/null
}

# run_claude CFG NAME [ARGS...] runs one print-mode session in REPO.
run_claude() {
  local cfg="$1" name="$2"
  shift 2
  (cd "$REPO" && CLAUDE_CONFIG_DIR="$cfg" CLAUDE_PLUGIN_DATA="${cfg}/clued-data" \
    "$CLAUDE_BIN" -p --output-format json --debug hooks --debug-file "${WORK}/${name}.debug" "$@" "$PROMPT") \
    > "${WORK}/${name}.json" 2> "${WORK}/${name}.stderr"
}

# missing_paths CFG prints every SessionStart command path (its first word,
# with CLAUDE_PLUGIN_ROOT expanded) that does not exist.
missing_paths() {
  local cfg="$1" root cmd path
  root="$(installed_root "$cfg")"
  {
    jq -r '.hooks.SessionStart[]?.hooks[]?.command' "${cfg}/settings.json" 2>/dev/null
    [[ -n "$root" ]] && jq -r '.hooks.SessionStart[]?.hooks[]?.command' "${root}/hooks/hooks.json" 2>/dev/null \
      | sed "s|\${CLAUDE_PLUGIN_ROOT}|${root}|g"
  } | while IFS= read -r cmd; do
    path="${cmd%% *}"
    [[ -e "$path" ]] || echo "$path"
  done
}

FAILED=0
ROWS=()
# report NAME LABEL SOURCE tabulates one run; SOURCE is the session source
# (startup, resume) whose blocks count as this run's copies.
report() {
  local name="$1" label="$2" source="$3" out="${WORK}/${1}.json" reply ran seen copies missing
  reply="$(jq -r '.result // empty' "$out" 2>/dev/null)"
  # The debug log has one "Hooks: SessionStart:<source> [<command>] finished
  # with status N" line per hook run.
  ran="$(grep -cE "SessionStart:${source} \[[^]]* hook prime\] finished" "${WORK}/${name}.debug" 2>/dev/null)"
  seen=no
  grep -q '^clued prime report for' <<<"$reply" && seen=yes
  copies="$(grep -c "^clued prime report for .* (session ${source})" <<<"$reply")"
  missing="$(missing_paths "${WORK}/${name%%-*}/config" | tr '\n' ' ')"
  ROWS+=("${VERSION} | ${label} | ${ran} | ${seen} | ${copies}${missing:+ | missing: ${missing}}")
  if [[ "$copies" != 1 || -n "$missing" ]]; then
    FAILED=1
    echo "verify-prime: ${label}: reply was: ${reply:-$(head -c 400 "${WORK}/${name}.stderr" 2>/dev/null)}" >&2
  fi
}

# A: the plugin's hooks/hooks.json only.
cfgA="$(new_config A)"
if install_plugin "$cfgA"; then
  run_claude "$cfgA" A
  report A "A plugin" startup
  # D: A resumed.
  sid="$(jq -r '.session_id // empty' "${WORK}/A.json" 2>/dev/null)"
  if [[ -n "$sid" ]]; then
    run_claude "$cfgA" A-resume --resume "$sid"
    report A-resume "D resume" resume
  else
    ROWS+=("${VERSION} | D resume | - | no | 0 | A gave no session id")
    FAILED=1
  fi
else
  ROWS+=("${VERSION} | A plugin | - | no | 0 | plugin install failed")
  ROWS+=("${VERSION} | D resume | - | no | 0 | plugin install failed")
  FAILED=1
fi

# B: the settings mirror only, pointing at this checkout.
cfgB="$(new_config B)"
CLAUDE_CONFIG_DIR="$cfgB" CLAUDE_PLUGIN_DATA="${cfgB}/clued-data" "${ROOT}/hooks/register-hooks" --settings >/dev/null
run_claude "$cfgB" B
report B "B settings" startup

# C: an upgrade, the installed plugin plus the mirror an older clued wrote.
cfgC="$(new_config C)"
if install_plugin "$cfgC"; then
  rootC="$(installed_root "$cfgC")"
  jq --arg root "$rootC" '{hooks: (.hooks | map_values(map(.hooks |= map(.command |= gsub("\\$\\{CLAUDE_PLUGIN_ROOT\\}"; $root)))))}' \
    "${rootC}/hooks/hooks.json" > "${cfgC}/settings.json"
  CLAUDE_CONFIG_DIR="$cfgC" CLAUDE_PLUGIN_DATA="${cfgC}/clued-data" "${rootC}/hooks/register-hooks" >/dev/null
  run_claude "$cfgC" C
  report C "C upgrade" startup
else
  ROWS+=("${VERSION} | C upgrade | - | no | 0 | plugin install failed")
  FAILED=1
fi

echo "claude --version | config | hook ran | context seen | copies"
printf '%s\n' "${ROWS[@]}"
exit "$FAILED"
