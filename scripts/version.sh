#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Single source of truth: repo-root VERSION (SemVer X.Y.Z).
#
#   scripts/version.sh              # print VERSION
#   scripts/version.sh set X.Y.Z    # write VERSION and stamp derived files
#   scripts/version.sh bump patch|minor|major
#   scripts/version.sh check        # exit 1 if any stamped file disagrees
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
SEMVER_RE='^[0-9]+\.[0-9]+\.[0-9]+$'

README="$ROOT/README.md"
PKG_JSON="$ROOT/web/package.json"
OPENAPI="$ROOT/api/openapi.yaml"
CHART="$ROOT/deploy/helm/featherdesk/Chart.yaml"
VERSION_FILE="$ROOT/VERSION"

sed_inplace() {
  local file=$1 expr=$2
  local tmp
  tmp=$(mktemp)
  sed -E "$expr" "$file" >"$tmp"
  mv "$tmp" "$file"
}

read_version() {
  local v
  v=$(tr -d '[:space:]' <"$VERSION_FILE")
  if [[ ! $v =~ $SEMVER_RE ]]; then
    echo "invalid VERSION file (want SemVer X.Y.Z): $v" >&2
    exit 1
  fi
  printf '%s' "$v"
}

validate_semver() {
  if [[ ! $1 =~ $SEMVER_RE ]]; then
    echo "invalid SemVer (want X.Y.Z): $1" >&2
    exit 1
  fi
}

stamp_files() {
  local ver=$1 date=$2
  printf '%s\n' "$ver" >"$VERSION_FILE"
  sed_inplace "$README" "s/^\*\*Version [0-9]+\.[0-9]+\.[0-9]+\*\* \([0-9]{4}-[0-9]{2}-[0-9]{2}\):/**Version ${ver}** (${date}):/"
  sed_inplace "$PKG_JSON" 's/"version": "[^"]+"/"version": "'"$ver"'"/'
  sed_inplace "$OPENAPI" 's/^  version: "[^"]+"/  version: "'"$ver"'"/'
  sed_inplace "$CHART" "s/^version: .+/version: ${ver}/"
  sed_inplace "$CHART" 's/^appVersion: .+/appVersion: "'"$ver"'"/'
}

file_version_readme() {
  sed -nE 's/^\*\*Version ([0-9]+\.[0-9]+\.[0-9]+)\*\*.*/\1/p' "$README" | head -1
}

file_version_pkg() {
  sed -nE 's/^  "version": "([^"]+)".*/\1/p' "$PKG_JSON" | head -1
}

file_version_openapi() {
  sed -nE 's/^  version: "([^"]+)".*/\1/p' "$OPENAPI" | head -1
}

file_version_chart() {
  sed -nE 's/^version: (.+)$/\1/p' "$CHART" | head -1
}

file_app_version_chart() {
  sed -nE 's/^appVersion: "([^"]+)".*/\1/p' "$CHART" | head -1
}

check_one() {
  local label=$1 got=$2 want=$3
  if [[ $got != "$want" ]]; then
    echo "$label: $got (want $want)" >&2
    return 1
  fi
  return 0
}

cmd_check() {
  local want fail=0
  want=$(read_version)
  check_one README.md "$(file_version_readme)" "$want" || fail=1
  check_one web/package.json "$(file_version_pkg)" "$want" || fail=1
  check_one api/openapi.yaml "$(file_version_openapi)" "$want" || fail=1
  check_one 'deploy/helm/featherdesk/Chart.yaml version' "$(file_version_chart)" "$want" || fail=1
  check_one 'deploy/helm/featherdesk/Chart.yaml appVersion' "$(file_app_version_chart)" "$want" || fail=1
  if [[ $fail -ne 0 ]]; then
    exit 1
  fi
  echo "ok: all stamped files match VERSION ($want)"
}

bump_part() {
  local kind=$1 cur=$2
  local a b c
  IFS=. read -r a b c <<<"$cur"
  case $kind in
    patch) printf '%s.%s.%s' "$a" "$b" $((c + 1)) ;;
    minor) printf '%s.%s.0' "$a" $((b + 1)) ;;
    major) printf '%s.0.0' $((a + 1)) ;;
    *)
      echo "usage: $0 bump patch|minor|major" >&2
      exit 1
      ;;
  esac
}

cmd=${1:-print}
case $cmd in
  print | '')
    read_version
    echo
    ;;
  set)
    [[ $# -eq 2 ]] || {
      echo "usage: $0 set X.Y.Z" >&2
      exit 1
    }
    validate_semver "$2"
    stamp_files "$2" "$(date +%Y-%m-%d)"
    echo "set version to $2"
    ;;
  bump)
    [[ $# -eq 2 ]] || {
      echo "usage: $0 bump patch|minor|major" >&2
      exit 1
    }
    next=$(bump_part "$2" "$(read_version)")
    validate_semver "$next"
    stamp_files "$next" "$(date +%Y-%m-%d)"
    echo "bumped to $next"
    ;;
  check)
    cmd_check
    ;;
  *)
    echo "usage: $0 | set X.Y.Z | bump patch|minor|major | check" >&2
    exit 1
    ;;
esac
