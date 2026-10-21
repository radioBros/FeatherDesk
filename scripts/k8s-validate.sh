#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-023 static checks: helm lint, helm template (default, Postgres+S3+ingress),
# the replicaCount > 1 refusal, and kubeconform -strict on the plain manifests
# and the rendered chart. Uses local helm/kubeconform when installed, otherwise
# the alpine/helm and ghcr.io/yannh/kubeconform images.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
K8S_VERSION=${K8S_VERSION:-1.34.0}

helm() {
  if type -P helm >/dev/null; then command helm "$@"
  else docker run --rm -v "$ROOT/deploy:/deploy" -w /deploy alpine/helm:3.19.0 "$@"; fi
}
kubeconform() { # reads manifests from stdin
  if type -P kubeconform >/dev/null; then command kubeconform "$@"
  else docker run --rm -i ghcr.io/yannh/kubeconform:v0.7.0 "$@"; fi
}
chart=helm/simpletickets
[ -d "$ROOT/deploy/$chart" ] || { echo "chart missing" >&2; exit 2; }
cd "$ROOT/deploy"

echo "== helm lint"
helm lint "$chart"

echo "== helm template (defaults)"
helm template st "$chart" --namespace simpletickets >"$OUT/default.yaml"
echo "== helm template (postgres + s3 + ingress + existing secret)"
helm template st "$chart" --namespace simpletickets \
  --set secret.existingSecret=st-secret --set database.existingSecret=st-pg \
  --set blob.url='s3://bucket/st?region=eu-west-1' --set blob.existingSecret=st-s3 \
  --set ingress.enabled=true >"$OUT/full.yaml"

grep -q 'type: Recreate' "$OUT/default.yaml" || { echo "Deployment strategy is not Recreate" >&2; exit 1; }
grep -q 'readOnlyRootFilesystem: true' "$OUT/default.yaml" || { echo "root fs not read-only" >&2; exit 1; }

echo "== replicaCount=2 must be refused"
if helm template st "$chart" --set replicaCount=2 >/dev/null 2>"$OUT/err"; then
  echo "chart rendered with replicaCount=2" >&2; exit 1
fi
grep -q 'replicaCount must be 1' "$OUT/err" && echo "refused: $(grep -o 'replicaCount must be 1[^:]*' "$OUT/err" | head -1)"

echo "== kubeconform -strict (k8s $K8S_VERSION)"
for f in "$ROOT"/deploy/k8s/*.yaml "$OUT/default.yaml" "$OUT/full.yaml"; do
  kubeconform -strict -summary -kubernetes-version "$K8S_VERSION" - <"$f"
done
echo "ok"
