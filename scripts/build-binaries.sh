#!/usr/bin/env bash
# Cross-compiles mind-palace for every platform the plugin supports.
#
#   scripts/build-binaries.sh            gzip each binary into dist/ and write
#                                        dist/SHA256SUMS (the hashes of the
#                                        uncompressed binaries); commit the result
#   scripts/build-binaries.sh --raw DIR  write the uncompressed binaries and
#                                        SHA256SUMS into DIR (release assets)
#   scripts/build-binaries.sh --check    rebuild into a temporary directory and
#                                        fail when dist/SHA256SUMS differs,
#                                        i.e. the committed binaries are stale
#
# Builds are reproducible (CGO off, -trimpath, no VCS stamp, the Go toolchain
# pinned to the version in go.mod), so the same source always gives the same
# hashes. Gzip runs with -n so the archives carry no name or timestamp either.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="${ROOT}/mind-palace"
DIST="${ROOT}/dist"

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

MODE=dist
OUT=""
case "${1:-}" in
  "") ;;
  --raw)
    MODE=raw
    OUT="${2:?usage: $0 --raw DIR}"
    ;;
  --check) MODE=check ;;
  *)
    echo "usage: $0 [--raw DIR | --check]" >&2
    exit 2
    ;;
esac

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

# build_all DIR writes one binary per target plus SHA256SUMS into DIR.
build_all() {
  local dir="$1" target goos goarch name
  mkdir -p "$dir"
  : > "${dir}/SHA256SUMS"
  for target in "${TARGETS[@]}"; do
    goos="${target%/*}"
    goarch="${target#*/}"
    name="mind-palace_${goos}_${goarch}"
    [[ "$goos" == windows ]] && name+=".exe"
    echo "building ${name}" >&2
    (cd "$MODULE" && GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -buildvcs=false -ldflags '-s -w' -o "${dir}/${name}" ./cmd/mind-palace)
    printf '%s  %s\n' "$(sha256 "${dir}/${name}")" "$name" >> "${dir}/SHA256SUMS"
  done
}

case "$MODE" in
  raw)
    build_all "$OUT"
    ;;
  dist)
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    build_all "$work"
    mkdir -p "$DIST"
    rm -f "${DIST}"/mind-palace_*
    for bin in "${work}"/mind-palace_*; do
      gzip -9 -n -c "$bin" > "${DIST}/$(basename "$bin").gz"
    done
    cp "${work}/SHA256SUMS" "${DIST}/SHA256SUMS"
    echo "wrote $(ls "${DIST}"/mind-palace_* | wc -l | tr -d ' ') archives to ${DIST}" >&2
    ;;
  check)
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT
    build_all "$work"
    if ! diff -u "${DIST}/SHA256SUMS" "${work}/SHA256SUMS"; then
      echo "build-binaries: dist is out of date; run scripts/build-binaries.sh and commit dist" >&2
      exit 1
    fi
    # The archives themselves must unpack to the hashed binaries, or the shim
    # will reject them on the user's machine.
    while read -r sum name; do
      gz="${DIST}/${name}.gz"
      [[ -f "$gz" ]] || { echo "build-binaries: ${gz} is missing" >&2; exit 1; }
      gzip -dc "$gz" > "${work}/unpacked" || { echo "build-binaries: ${gz} does not unpack" >&2; exit 1; }
      actual="$(sha256 "${work}/unpacked")"
      [[ "$actual" == "$sum" ]] || { echo "build-binaries: ${gz} unpacks to ${actual}, SHA256SUMS says ${sum}" >&2; exit 1; }
    done < "${DIST}/SHA256SUMS"
    echo "dist matches the source" >&2
    ;;
esac
