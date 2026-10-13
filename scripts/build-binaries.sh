#!/usr/bin/env bash
# Cross-compiles mind-palace for every platform the plugin supports.
#
#   scripts/build-binaries.sh [DIR]   write mind-palace_<os>_<arch>[.exe] for
#                                     every target plus SHA256SUMS into DIR
#                                     (default: dist/, which git ignores)
#
# Nothing it produces is committed: CI runs it to prove every target still
# compiles, and the release job of .github/workflows/mind-palace.yml runs it
# and attaches the output to the GitHub release that scripts/clued downloads
# from (the `version` in .claude-plugin/plugin.json names that release).
# Builds are reproducible (CGO off, -trimpath, no VCS stamp, the Go toolchain
# pinned to the version in go.mod), so the same source always gives the same
# hashes. The version from the manifest (or VERSION in the environment) is
# stamped into the binary as remote.Version, which the server reports from
# /health and the client sends in its User-Agent. The targets compile
# concurrently (BUILD_JOBS limits how many at once).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="${ROOT}/mind-palace"
VERSION="${VERSION:-$(sed -nE 's/^ *"version": *"([^"]+)".*/\1/p' "${ROOT}/.claude-plugin/plugin.json" | head -n 1)}"
LDFLAGS="-s -w -X github.com/robbiebyrd/clued/mind-palace/remote.Version=${VERSION:-dev}"

# GOOS/GOARCH pairs. Go has no windows/arm (32-bit) target, and Claude Code
# does not run on 32-bit x86, so neither is built. linux/arm is built for
# ARMv6 (GOARM=6) so it runs on armv6l hosts such as older Raspberry Pis as
# well as on ARMv7.
TARGETS=(
  darwin/amd64
  darwin/arm64
  linux/amd64
  linux/arm64
  linux/arm
  windows/amd64
  windows/arm64
)

case "${1:-}" in
  -h|--help)
    echo "usage: $0 [DIR]" >&2
    exit 2
    ;;
esac
OUT="${1:-${ROOT}/dist}"
# The builds run from the module directory, so the output directory must be
# absolute or a relative DIR would land under mind-palace/.
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

command -v go >/dev/null || { echo "build-binaries: go is not installed" >&2; exit 1; }
GO_VERSION="$(awk '/^go /{print $2; exit}' "${MODULE}/go.mod")"
export GOTOOLCHAIN="go${GO_VERSION}"
export CGO_ENABLED=0
export GOFLAGS=-mod=mod
export GOARM=6

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# asset_name prints the binary name of a GOOS/GOARCH pair.
asset_name() {
  local name="mind-palace_${1}_${2}"
  [[ "$1" == windows ]] && name+=".exe"
  echo "$name"
}

rm -f "${OUT}"/mind-palace_* "${OUT}/SHA256SUMS"

# The targets build concurrently: each compile is mostly a single-threaded
# link, so running them side by side uses every core (BUILD_JOBS caps how
# many run at once; the default is all of them). The Go build cache is safe
# for concurrent use, and the module download finished during the first
# `go build` of the warm-up below, so the parallel builds never race on it.
(cd "$MODULE" && go mod download) >&2
JOBS="${BUILD_JOBS:-${#TARGETS[@]}}"
declare -A pids
failed=0
for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="$(asset_name "$goos" "$goarch")"
  while (( $(jobs -rp | wc -l) >= JOBS )); do
    sleep 0.2
  done
  echo "building ${name}" >&2
  (cd "$MODULE" && GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "${OUT}/${name}" ./cmd/mind-palace) &
  pids["$name"]=$!
done
for name in "${!pids[@]}"; do
  if ! wait "${pids[$name]}"; then
    echo "build-binaries: ${name} failed" >&2
    failed=1
  fi
done
(( failed == 0 )) || exit 1

# SHA256SUMS lists the targets in their declared order whatever finished first.
: > "${OUT}/SHA256SUMS"
for target in "${TARGETS[@]}"; do
  name="$(asset_name "${target%/*}" "${target#*/}")"
  sum="$(sha256 "${OUT}/${name}")" || { echo "build-binaries: ${OUT}/${name} is missing" >&2; exit 1; }
  printf '%s  %s\n' "$sum" "$name" >> "${OUT}/SHA256SUMS"
done
echo "wrote ${#TARGETS[@]} binaries (v${VERSION:-dev}) and SHA256SUMS to ${OUT}" >&2
