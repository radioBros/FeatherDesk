#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-031: test-only containers (MinIO, Dovecot, Mailpit) must never reach the
# production packaging. Fails when any of them appears in compose.yml,
# compose.postgres.yml, compose.litestream.yml (+ its config), the plain k8s
# manifests or the Helm chart.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PROD=("$ROOT/deploy/compose.yml" "$ROOT/deploy/compose.postgres.yml" "$ROOT/deploy/compose.litestream.yml" "$ROOT/deploy/litestream" "$ROOT/deploy/k8s" "$ROOT/deploy/helm")
PATTERN='minio|dovecot|mailpit|compose\.test\.yml'
# Comment lines (#, {{/* */}}) may name them for documentation; config may not.
hits=$(grep -rniE "$PATTERN" "${PROD[@]}" 2>/dev/null | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(#|\{\{-? */\*)' || true)
if [ -n "$hits" ]; then
  echo "test-only images/files referenced by production packaging:" >&2
  echo "$hits" >&2
  exit 1
fi
echo "ok: no test-only image in compose.yml, compose.postgres.yml, k8s or helm"
