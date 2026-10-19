#!/usr/bin/env bash
# Runs the code search evaluation (plan 0012-cgi, 5.1): every task of
# mind-palace/eval/codesearch/tasks.yaml in a headless `claude -p` session,
# in each arm (grep: grep and read only; graph: plus the code graph tools;
# context: plus code_context only), three times by default, then reports
# tokens, turns, tool calls and success per task family and per language,
# read from clued's own capture of those sessions (`session usage`,
# `session activities`). Manual: the sessions are paid, so CI never runs it;
# it runs `go test -run TestEvalLive` with CLUED_EVAL=1. Record the report in
# plan 0012-cgi's work log.
#
#   scripts/eval-codesearch.sh [--tasks <regexp>] [--arms grep,graph,context]
#       [--runs <n>] [--model <model>] [--budget <usd>] [--cloud-dir <dir>]
#       [--out <dir>] [--cache <dir>] [--claude <bin>] [--plugin-dir <dir>]
#       [--tool-prefix <prefix>] [--config <file>] [--keep]
#   scripts/eval-codesearch.sh --verify-only [--cloud-dir <dir>] [--cache <dir>]
#
#   --tasks <re>        only the tasks whose id matches (default: all)
#   --arms <list>       arms to run (default: grep,graph,context)
#   --runs <n>          runs per task and arm (default: 3)
#   --model <model>     the model of every session (default: Claude Code's)
#   --budget <usd>      spending cap of one session (default: 2)
#   --cloud-dir <dir>   a clued-cloud checkout; without it its tasks are skipped
#   --out <dir>         where runs.jsonl, report.md and report.json go
#                       (default: a new directory under the work directory)
#   --cache <dir>       clones of the pinned corpus, kept between runs
#                       (default: ~/.cache/clued-eval/corpus)
#   --claude <bin>      the Claude Code binary (default: claude on PATH)
#   --plugin-dir <dir>  load the clued plugin from this directory in each
#                       session (default: the installed plugin)
#   --tool-prefix <p>   MCP prefix of the clued tools in a session
#                       (default: mcp__plugin_clued_clued__)
#   --config <file>     the clued config the capture is read with
#   --keep              keep every checkout the sessions ran in
#   --verify-only       check the ground truth instead: the clued tasks
#                       against this checkout, clued-cloud's against
#                       --cloud-dir, the corpus's against its pinned commits
#                       (fetched); needs no credentials
#
# Every session runs in a fresh clone (clued and clued-cloud at their HEAD,
# the corpus at the pinned commit) whose origin is the source's, so the
# capture and the code graph see the project the source is recorded under.
# The graph and context arms need remote mode (a clued server or Clued Cloud
# with the code graph) and the projects indexed; the grep arm runs anywhere
# clued captures sessions.
#
# Exits 0 when the evaluation ran (or the ground truth holds), 1 when it
# failed, 2 on a usage error and 3 when there are no Claude credentials.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
usage() {
  echo "usage: $0 [--tasks re] [--arms list] [--runs n] [--model m] [--budget usd] [--cloud-dir dir] [--out dir] [--cache dir] [--claude bin] [--plugin-dir dir] [--tool-prefix p] [--config file] [--keep] | --verify-only [--cloud-dir dir] [--cache dir]" >&2
  exit 2
}
need() { [[ -n "${2:-}" ]] || usage; }

CLAUDE_BIN=claude
TASKS="" ARMS="" RUNS="" MODEL="" BUDGET="" CLOUD_DIR="" OUT="" CACHE="" PLUGIN_DIR="" PREFIX="" CONFIG=""
KEEP=0
VERIFY=0
while (( $# > 0 )); do
  case "$1" in
    --tasks) need "$1" "${2:-}"; TASKS="$2"; shift 2 ;;
    --arms) need "$1" "${2:-}"; ARMS="$2"; shift 2 ;;
    --runs) need "$1" "${2:-}"; [[ "$2" =~ ^[1-9][0-9]*$ ]] || usage; RUNS="$2"; shift 2 ;;
    --model) need "$1" "${2:-}"; MODEL="$2"; shift 2 ;;
    --budget) need "$1" "${2:-}"; [[ "$2" =~ ^[0-9]+(\.[0-9]+)?$ ]] || usage; BUDGET="$2"; shift 2 ;;
    --cloud-dir) need "$1" "${2:-}"; CLOUD_DIR="$(cd "$2" 2>/dev/null && pwd)" || { echo "eval-codesearch: no directory $2" >&2; exit 2; }; shift 2 ;;
    --out) need "$1" "${2:-}"; OUT="$2"; shift 2 ;;
    --cache) need "$1" "${2:-}"; CACHE="$2"; shift 2 ;;
    --claude) need "$1" "${2:-}"; CLAUDE_BIN="$2"; shift 2 ;;
    --plugin-dir) need "$1" "${2:-}"; PLUGIN_DIR="$2"; shift 2 ;;
    --tool-prefix) need "$1" "${2:-}"; PREFIX="$2"; shift 2 ;;
    --config) need "$1" "${2:-}"; CONFIG="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --verify-only) VERIFY=1; shift ;;
    -h|--help) sed -n '2,48p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) usage ;;
  esac
done

command -v go >/dev/null || { echo "eval-codesearch: go not found" >&2; exit 2; }
command -v git >/dev/null || { echo "eval-codesearch: git not found" >&2; exit 2; }
[[ -n "$CACHE" ]] || CACHE="${XDG_CACHE_HOME:-${HOME}/.cache}/clued-eval/corpus"
mkdir -p "$CACHE" || exit 2
CACHE="$(cd "$CACHE" && pwd)"

if (( VERIFY )); then
  cd "${ROOT}/mind-palace" || exit 1
  CLUED_EVAL_VERIFY=1 CLUED_EVAL_CACHE="$CACHE" CLUED_EVAL_CLOUD_DIR="$CLOUD_DIR" \
    go test -count=1 -timeout 0 -v -run '^(TestCluedGroundTruth|TestCloudGroundTruth|TestCorpusGroundTruth)$' ./eval/codesearch/
  exit $?
fi

command -v "$CLAUDE_BIN" >/dev/null || { echo "eval-codesearch: $CLAUDE_BIN not found" >&2; exit 2; }

# The credentials the sessions inherit: the config dir's file, or a token or
# key in the environment (on macOS the login lives in the keychain, so export
# CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token`).
SRC_CFG="${CLAUDE_CONFIG_DIR:-${HOME}/.claude}"
if [[ ! -f "${SRC_CFG}/.credentials.json" && -z "${ANTHROPIC_API_KEY:-}" && -z "${ANTHROPIC_AUTH_TOKEN:-}" \
  && -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" && -z "${ANTHROPIC_BASE_URL:-}" ]]; then
  echo "eval-codesearch: no Claude credentials (no ${SRC_CFG}/.credentials.json and none of ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_BASE_URL); run \`claude setup-token\` and export CLAUDE_CODE_OAUTH_TOKEN" >&2
  exit 3
fi

# A run started from inside a Claude Code session must start sessions of its
# own: these tie a child to its parent's session, transcript and sockets.
for v in CLAUDECODE CLAUDE_CODE_SESSION_ID CLAUDE_CODE_REMOTE_SESSION_ID CLAUDE_CODE_CHILD_SESSION \
  CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_SESSION_ATTENDED CLAUDE_CODE_WORKER_EPOCH CLAUDE_PID \
  SESSION_INGRESS_URL CLAUDE_SESSION_INGRESS_TOKEN_FILE CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2 \
  CLAUDE_CODE_MESSAGING_SOCKET CLAUDE_CODE_MESSAGING_TOKEN CLAUDE_CODE_SYNC_SESSION_REFS \
  CLAUDE_CODE_TEE_SDK_STDOUT CLAUDE_CODE_DIAGNOSTICS_FILE CLAUDE_CODE_REMOTE_SDK_URL \
  CLAUDE_CODE_SYNC_PLUGINS CLAUDE_CODE_SYNC_SKILLS CLAUDE_PLUGIN_ROOT CLAUDE_PLUGIN_DATA; do
  unset "$v"
done

WORK="$(mktemp -d "${TMPDIR:-/tmp}/clued-eval.XXXXXX")" || exit 1
[[ -n "$OUT" ]] || OUT="${WORK}/report"
mkdir -p "$OUT" || exit 2
OUT="$(cd "$OUT" && pwd)"

# The capture is read with this checkout's mind-palace, so the report never
# depends on which release the shim would pick.
MP="${WORK}/mind-palace"
(cd "${ROOT}/mind-palace" && go build -o "$MP" ./cmd/mind-palace) || { echo "eval-codesearch: building mind-palace failed" >&2; exit 1; }

CFG_FILE="${CONFIG:-${HOME}/.claude/plugins/data/clued/config.json}"
if [[ -z "${CLUED_SERVER_URL:-}" ]] && ! grep -q '"serverUrl"[[:space:]]*:[[:space:]]*"[^"]' "$CFG_FILE" 2>/dev/null; then
  echo "eval-codesearch: clued is not in remote mode (no serverUrl in ${CFG_FILE}); the graph and context arms will find no code tools" >&2
fi

echo "eval-codesearch: claude $("$CLAUDE_BIN" --version 2>/dev/null | head -1), work in ${WORK}, report in ${OUT}" >&2

cd "${ROOT}/mind-palace" || exit 1
CLUED_EVAL=1 \
CLUED_EVAL_CLAUDE="$CLAUDE_BIN" \
CLUED_EVAL_MIND_PALACE="$MP" \
CLUED_EVAL_CONFIG="$CONFIG" \
CLUED_EVAL_CLUED_DIR="$ROOT" \
CLUED_EVAL_CLOUD_DIR="$CLOUD_DIR" \
CLUED_EVAL_CACHE="$CACHE" \
CLUED_EVAL_WORK="$WORK" \
CLUED_EVAL_OUT="$OUT" \
CLUED_EVAL_TASKS="$TASKS" \
CLUED_EVAL_ARMS="$ARMS" \
CLUED_EVAL_RUNS="$RUNS" \
CLUED_EVAL_MODEL="$MODEL" \
CLUED_EVAL_BUDGET_USD="$BUDGET" \
CLUED_EVAL_PLUGIN_DIR="$PLUGIN_DIR" \
CLUED_EVAL_TOOL_PREFIX="$PREFIX" \
CLUED_EVAL_KEEP="$KEEP" \
  go test -count=1 -timeout 0 -v -run '^TestEvalLive$' ./eval/codesearch/
status=$?

[[ -f "${OUT}/report.md" ]] && cat "${OUT}/report.md"
(( KEEP )) || rm -rf "${WORK}/runs" "${MP}"
exit $(( status == 0 ? 0 : 1 ))
