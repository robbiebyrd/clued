#!/usr/bin/env bash
# Cross-compiles mind-palace for every platform the plugin supports.
#
#   scripts/build-binaries.sh [DIR]   write mind-palace_<os>_<arch>[.exe] for
#                                     every target plus SHA256SUMS into DIR
#                                     (default: dist/, which git ignores)
#
# Nothing it produces is committed: CI runs it to prove every target still
# compiles, and .github/workflows/release.yml runs it on a v* tag and attaches
# the output to the GitHub release that scripts/clued downloads from (the
# `version` in .claude-plugin/plugin.json names that release). Builds are
# reproducible (CGO off, -trimpath, no VCS stamp, the Go toolchain pinned to
# the version in go.mod), so the same source always gives the same hashes.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="${ROOT}/mind-palace"

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

mkdir -p "$OUT"
rm -f "${OUT}"/mind-palace_* "${OUT}/SHA256SUMS"
: > "${OUT}/SHA256SUMS"
for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="mind-palace_${goos}_${goarch}"
  [[ "$goos" == windows ]] && name+=".exe"
  echo "building ${name}" >&2
  (cd "$MODULE" && GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -buildvcs=false -ldflags '-s -w' -o "${OUT}/${name}" ./cmd/mind-palace)
  printf '%s  %s\n' "$(sha256 "${OUT}/${name}")" "$name" >> "${OUT}/SHA256SUMS"
done
echo "wrote ${#TARGETS[@]} binaries and SHA256SUMS to ${OUT}" >&2
