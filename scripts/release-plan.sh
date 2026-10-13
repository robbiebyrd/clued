#!/usr/bin/env bash
# Decides what a run of the Mind palace workflow releases, so the job that
# builds the binaries (and stamps the version into them) and the job that
# publishes them agree.
#
#   scripts/release-plan.sh EVENT    EVENT is push (a push to main),
#                                    dispatch (a manual run on main) or
#                                    other (anything else: nothing is
#                                    released)
#
# Prints key=value lines for $GITHUB_OUTPUT:
#   version  the version to stamp and release
#   action   none | publish (a version with no release yet) | complete (a
#            release missing assets) | bump (a push to main whose version is
#            released: release the next unreleased patch version)
#   exists   whether the release already exists (complete only)
# It reads .claude-plugin/plugin.json and, except for "other", the releases
# through `gh` and the subject of HEAD (a "Release v…" bump commit releases
# nothing more).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
event="${1:-}"
case "$event" in
  push|dispatch|other) ;;
  *) echo "usage: $0 push|dispatch|other" >&2; exit 2 ;;
esac

version=$(jq -r '.version // empty' "${ROOT}/.claude-plugin/plugin.json")
if [[ -z "$version" ]]; then
  echo "release-plan: no version in .claude-plugin/plugin.json" >&2
  exit 1
fi

emit() { printf 'version=%s\naction=%s\nexists=%s\n' "$1" "$2" "$3"; }

if [[ "$event" == other ]]; then
  emit "$version" none false
  exit 0
fi

bump() { awk -F. '{printf "%d.%d.%d", $1, $2, $3 + 1}' <<<"$1"; }
assets() { gh release view "v$1" --json assets --jq '.assets | length' 2>/dev/null; }
# A release counts as published when it carries every asset the build
# script produces: its targets plus SHA256SUMS.
want=$(( $("${ROOT}/scripts/build-binaries.sh" --targets | wc -l) + 1 ))

if ! n=$(assets "$version"); then
  echo "release-plan: v$version has no release yet; publishing it" >&2
  emit "$version" publish false
elif (( n < want )); then
  echo "release-plan: v$version has $n of $want assets; completing it" >&2
  emit "$version" complete true
elif [[ "$event" != push ]]; then
  echo "release-plan: v$version is released; a manual run bumps nothing" >&2
  emit "$version" none true
elif git -C "$ROOT" log -1 --format=%s | grep -q '^Release v'; then
  echo "release-plan: HEAD is the bump to v$version, which is released" >&2
  emit "$version" none true
else
  next=$(bump "$version")
  while assets "$next" >/dev/null; do next=$(bump "$next"); done
  echo "release-plan: v$version is released; the next release is v$next" >&2
  emit "$next" bump false
fi
