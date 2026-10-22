#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Dependency gate (DEP-011, CONVENTIONS §10): the modules that go.mod requires
# directly AND that are linked into the shipped binary are the "runtime"
# dependencies: at most MAX (15; target 14). No AWS SDK anywhere in the graph
# (S3 is hand-written SigV4, D-17).
set -euo pipefail
MAX=${MAX:-15}
cd "$(dirname "$0")/.."
self=$(go list -m)
direct=$(go list -m -f '{{if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all | sed '/^$/d' | sort -u)
linked=$(go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./cmd/featherdesk | sed '/^$/d' | grep -vx "$self" | sort -u)
runtime=$(comm -12 <(echo "$direct") <(echo "$linked"))
n=$(echo "$runtime" | sed '/^$/d' | wc -l | tr -d ' ')
echo "runtime Go modules (direct and linked into ./cmd/featherdesk): $n (limit $MAX)"
echo "$runtime" | sed 's/^/  /'
echo "test/tool-only direct modules: $(comm -23 <(echo "$direct") <(echo "$linked") | tr '\n' ' ')"
fail=0
if [ "$n" -gt "$MAX" ]; then echo "FAIL: more than $MAX runtime modules" >&2; fail=1; fi
if go list -m all | grep -qiE '^github\.com/aws/'; then
  echo "FAIL: AWS SDK in the module graph:" >&2; go list -m all | grep -iE '^github\.com/aws/' >&2; fail=1
fi
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "Runtime Go modules: **$n** / $MAX (target 14)" >>"$GITHUB_STEP_SUMMARY"
exit $fail
