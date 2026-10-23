#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Which parts of CI a change needs, as `name=true|false` lines (for
# $GITHUB_OUTPUT). Reads the changed files (scripts/ci-changed-files.sh) on
# stdin; only the line ALL (a manual or nightly run, or the `ci:full` PR
# label) turns everything on. Editing CI itself does not: owner rule, CI tests
# only the affected parts (a CI change shows its effect on the jobs it gates).
#
#   go      Go code, migrations, the API spec, go.mod     → go, golangci, openapi
#   web     the SPA                                       → web, e2e
#   api     the API spec or the generated client          → openapi
#   docs    the manual and the files its checks read      → docs (also on go: doc tests hit the API)
#   deploy  Dockerfile, compose, Helm, proxy/smoke scripts → image, kind, proxies, smoke
#   store   the database layer                            → e2e also on Postgres
set -euo pipefail

files=$(cat)
has() { printf '%s\n' "$files" | grep -qE "$1"; }

all=false
if has '^ALL$'; then all=true; fi

flag() { # name regex
  if [ "$all" = true ] || has "$2"; then echo "$1=true"; else echo "$1=false"; fi
}

flag go '^(cmd|internal|api|tools)/|^go\.(mod|sum)$|^web/embed\.go$'
flag web '^web/'
flag api '^api/|^web/src/api/|^web/scripts/gen-api'
flag docs '^docs/|\.md$|^THIRD_PARTY_NOTICES$|^NOTICE$|^lychee\.toml$|^scripts/(docs|doc-test|notices)'
flag deploy '^Dockerfile$|^\.dockerignore$|^deploy/|^scripts/(compose-smoke|footprint|kind|proxy-test|k8s)'
flag store '^internal/store/'
echo "all=$all"
