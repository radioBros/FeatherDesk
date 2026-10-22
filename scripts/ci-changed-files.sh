#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# The files this CI run is about, one per line, or the single line ALL when
# everything must run: the nightly and manual runs, a push whose base is
# unknown (new branch, force push), or a diff that cannot be computed.
#
#   pull_request: the PR's changes against its base branch (merge base)
#   push:         the pushed range (github.event.before .. github.sha)
#
# Needs a checkout with history (fetch-depth: 0). Reads the GitHub Actions
# environment: GITHUB_EVENT_NAME, GITHUB_BASE_REF, and BEFORE / GITHUB_SHA
# (BEFORE is github.event.before, passed in by the workflow).
set -euo pipefail

case "${GITHUB_EVENT_NAME:-}" in
  pull_request)
    git fetch --no-tags --quiet origin "${GITHUB_BASE_REF}" || { echo ALL; exit 0; }
    git diff --name-only "origin/${GITHUB_BASE_REF}...HEAD" || echo ALL
    ;;
  push)
    if [ -z "${BEFORE:-}" ] || [ "${BEFORE}" = 0000000000000000000000000000000000000000 ] || ! git cat-file -e "${BEFORE}^{commit}" 2>/dev/null; then
      echo ALL
    else
      git diff --name-only "${BEFORE}" "${GITHUB_SHA}" || echo ALL
    fi
    ;;
  *)
    echo ALL
    ;;
esac
