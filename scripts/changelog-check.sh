#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-019: a change that adds or edits a migration must carry a `### Migrations`
# note in the [Unreleased] section of CHANGELOG.md, and CHANGELOG.md itself must
# be part of the same change (so the note of an older release never satisfies a
# new PR).
#
#   scripts/changelog-check.sh <changed-files-list>     # one path per line
#   git diff --name-only "$BASE...$HEAD" | scripts/changelog-check.sh -
#
# CHANGELOG defaults to ./CHANGELOG.md (override with CHANGELOG=…).
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CHANGELOG=${CHANGELOG:-$ROOT/CHANGELOG.md}
LIST=${1:--}
if [ "$LIST" = - ]; then changed=$(cat); else changed=$(cat "$LIST"); fi

migrations=$(printf '%s\n' "$changed" | grep -E '^internal/store/migrations/(sqlite|postgres)/[^/]+\.sql$' || true)
if [ -z "$migrations" ]; then
  echo "ok: no migration changed"
  exit 0
fi
echo "migrations changed:"; printf '%s\n' "$migrations" | sed 's/^/  /'

fail=0
if ! printf '%s\n' "$changed" | grep -qx 'CHANGELOG.md'; then
  echo "::error::a migration changed but CHANGELOG.md did not; add a '### Migrations' entry under [Unreleased]" >&2
  fail=1
fi
[ -f "$CHANGELOG" ] || { echo "::error::$CHANGELOG missing" >&2; exit 1; }

# The [Unreleased] section runs from its heading to the next `## [` heading.
section=$(awk '/^## \[Unreleased\]/{on=1; next} on && /^## \[/{exit} on' "$CHANGELOG")
# `### Migrations` must exist there and be followed by at least one list item.
if ! printf '%s\n' "$section" | awk '/^### Migrations[[:space:]]*$/{m=1; next} m && /^### /{exit} m && /^[-*] +[^[:space:]]/{ok=1} END{exit ok?0:1}'; then
  echo "::error::CHANGELOG.md [Unreleased] has no '### Migrations' entry (a heading plus at least one '- …' line)" >&2
  fail=1
fi
[ "$fail" = 0 ] && echo "ok: CHANGELOG.md [Unreleased] documents the migration change"
exit "$fail"
