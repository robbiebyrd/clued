#!/usr/bin/env bash
# Fails unless every commit in BASE..HEAD carries a Developer Certificate of
# Origin sign-off: a "Signed-off-by: Name <email>" trailer whose e-mail is
# the commit author's (case-insensitive). Merge commits and commits authored
# by GitHub bots (…[bot]@users.noreply.github.com) are skipped. CONTRIBUTING.md
# ("Sign-off") explains how to sign off; .github/workflows/dco.yml runs this
# on every pull request.
#
#   scripts/check-dco.sh BASE HEAD
#
# Prints each unsigned commit and exits 1, or exits 0.
set -euo pipefail

if (( $# != 2 )); then
  echo "usage: $0 BASE HEAD" >&2
  exit 2
fi

missing=0
while IFS= read -r sha; do
  [[ -n "$sha" ]] || continue
  author="$(git log -1 --format=%ae "$sha")"
  [[ "$author" == *"[bot]@users.noreply.github.com" ]] && continue
  want="$(tr '[:upper:]' '[:lower:]' <<<"$author")"
  ok=0
  while IFS= read -r line; do
    email="$(sed -nE 's/.*<([^>]+)>.*/\1/p' <<<"$line" | tr '[:upper:]' '[:lower:]')"
    [[ -n "$email" && "$email" == "$want" ]] && ok=1
  done < <(git log -1 --format='%(trailers:key=Signed-off-by,valueonly)' "$sha")
  if (( ! ok )); then
    echo "check-dco: $(git log -1 --format='%h %s' "$sha") has no Signed-off-by for ${author} (git commit --amend -s, or git rebase --signoff)" >&2
    missing=1
  fi
done < <(git rev-list --no-merges "$1..$2")

if (( missing )); then
  echo "check-dco: sign off every commit; see CONTRIBUTING.md#sign-off" >&2
  exit 1
fi
echo "check-dco: every commit in $1..$2 is signed off" >&2
