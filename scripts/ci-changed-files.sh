#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# The files this CI run is about, one per line, or the single line ALL when
# everything must run: the nightly and manual runs, a pull request labelled
# `ci:full`, a push whose base is unknown (new branch, force push), or a diff
# that cannot be computed. Nothing at all for the push that merges a pull
# request: CI already tested that pull request.
#
#   pull_request: the PR's changes against its base branch (merge base)
#   push:         the pushed range (github.event.before .. github.sha)
#
# Needs a checkout with history (fetch-depth: 0). Reads the GitHub Actions
# environment: GITHUB_EVENT_NAME, GITHUB_BASE_REF, GITHUB_REPOSITORY,
# GITHUB_SHA, and from the workflow BEFORE (github.event.before), LABELS (the
# PR's labels, comma-separated) and GH_TOKEN (to look up merged PRs).
set -euo pipefail

case "${GITHUB_EVENT_NAME:-}" in
  pull_request)
    case ",${LABELS:-}," in *,ci:full,*) echo ALL; exit 0 ;; esac
    git fetch --no-tags --quiet origin "${GITHUB_BASE_REF}" || { echo ALL; exit 0; }
    git diff --name-only "origin/${GITHUB_BASE_REF}...HEAD" || echo ALL
    ;;
  push)
    # A merge of a tested pull request (merge, squash or rebase): nothing new to test.
    if [ -n "${GH_TOKEN:-}" ] && merged=$(gh api "repos/${GITHUB_REPOSITORY}/commits/${GITHUB_SHA}/pulls" \
        --jq '[.[] | select(.merged_at != null)] | length' 2>/dev/null) && [ "${merged:-0}" -gt 0 ]; then
      exit 0
    fi
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
