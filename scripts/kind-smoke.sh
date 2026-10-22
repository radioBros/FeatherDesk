#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-023 live check of the Helm chart on a throwaway kind cluster:
#   1. `helm install` (chart defaults; only the image, FD_PUBLIC_URL and a
#      bootstrap admin are set) → /readyz 200 within 60 s, then a WebSocket
#      ping → pong through `kubectl port-forward` (tools/loadtest -probe:
#      admin login, PAT, subprotocol featherdesk.v1).
#   2. the same with database.existingSecret → a Postgres 18 pod: /readyz,
#      ping/pong and `"engine":"postgres"` in the app log.
#   Both: restartCount 0 and readOnlyRootFilesystem true on the pod.
#   3. scripts/k8s-validate.sh (helm lint, replicaCount=2 refused, kubeconform).
# The cluster is always deleted on exit.
#
#   scripts/kind-smoke.sh --image featherdesk:local
#
# Needs: docker, kind, helm, kubectl (kind's), go.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
IMAGE=featherdesk:local
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    *) echo "usage: $0 [--image repo:tag]" >&2; exit 2 ;;
  esac
done
# Same Postgres as compose.postgres.yml / CI.
PG_IMAGE=${PG_IMAGE:-postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873}
PG_TAG=fd-kind-postgres:18
CLUSTER=fd-kind-$$
PORT1=${FD_KIND_PORT:-18300}
PORT2=$((PORT1 + 1))
ADMIN=admin@kind.test
PASS=kind-admin-password-123
WORK=$(mktemp -d)
KUBECONFIG=$WORK/kubeconfig
export KUBECONFIG
PF_PIDS=()

cleanup() {
  for p in "${PF_PIDS[@]+"${PF_PIDS[@]}"}"; do kill "$p" 2>/dev/null || true; done
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
command -v kubectl >/dev/null || { echo "kubectl not found" >&2; exit 2; }

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

step "build tools/loadtest (WebSocket probe)"
(cd "$ROOT" && go build -o "$WORK/loadtest" ./tools/loadtest)

step "kind cluster $CLUSTER"
kind create cluster --name "$CLUSTER" --wait 120s
kubectl version 2>/dev/null | sed -n 's/^Server Version: /server /p'
docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "image $IMAGE not found locally (build it first)"
docker image inspect "$PG_IMAGE" >/dev/null 2>&1 || docker pull "$PG_IMAGE" >/dev/null
docker tag "$PG_IMAGE" "$PG_TAG"
kind load docker-image --name "$CLUSTER" "$IMAGE" "$PG_TAG"

repo=${IMAGE%:*}; tag=${IMAGE##*:}
[ "$repo" = "$IMAGE" ] && tag=latest

# install <release> <namespace> <port> [extra helm args…] → time to /readyz, probe
install() {
  local rel=$1 ns=$2 port=$3; shift 3
  local t0 t1 pod
  t0=$(date +%s)
  helm install "$rel" "$ROOT/deploy/helm/featherdesk" --namespace "$ns" --create-namespace \
    --set image.repository="$repo" --set image.tag="$tag" --set image.pullPolicy=Never \
    --set env.FD_PUBLIC_URL="http://127.0.0.1:$port" \
    --set secret.values.FD_BOOTSTRAP_ADMIN_EMAIL="$ADMIN" \
    --set secret.values.FD_BOOTSTRAP_ADMIN_PASSWORD="$PASS" "$@" >/dev/null
  # The readiness probe is GET /readyz; the rollout completes when it answers 200.
  kubectl -n "$ns" rollout status deploy/"$rel"-featherdesk --timeout=60s \
    || { kubectl -n "$ns" describe pod; kubectl -n "$ns" logs deploy/"$rel"-featherdesk --tail=50 || true; fail "$rel: not ready within 60 s"; }
  t1=$(date +%s)
  echo "$rel: ready (readinessProbe /readyz) $((t1 - t0)) s after helm install"
  kubectl -n "$ns" port-forward svc/"$rel"-featherdesk "$port:80" >"$WORK/pf-$rel.log" 2>&1 &
  PF_PIDS+=($!)
  disown "$!" 2>/dev/null || true
  for _ in $(seq 1 50); do curl -fsS -o /dev/null --max-time 1 "http://127.0.0.1:$port/readyz" 2>/dev/null && break; sleep 0.2; done
  curl -fsS "http://127.0.0.1:$port/readyz" >/dev/null || fail "$rel: /readyz through port-forward"
  echo "$rel: GET /readyz through port-forward → $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/readyz")"
  "$WORK/loadtest" -probe -url "http://127.0.0.1:$port" -admin-email "$ADMIN" -admin-password "$PASS"
  pod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/instance="$rel" -o jsonpath='{.items[0].metadata.name}')
  local rc ro user
  rc=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].restartCount}')
  ro=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.spec.containers[0].securityContext.readOnlyRootFilesystem}')
  user=$(kubectl -n "$ns" get pod "$pod" -o jsonpath='{.spec.securityContext.runAsUser}')
  echo "$rel: pod $pod restartCount=$rc readOnlyRootFilesystem=$ro runAsUser=$user"
  [ "$rc" = 0 ] || fail "$rel: pod restarted ($rc)"
  [ "$ro" = true ] || fail "$rel: root filesystem is writable"
  LAST_POD=$pod
}

step "1. helm install, SQLite on the PVC (chart defaults)"
install st fd-sqlite "$PORT1"
kubectl -n fd-sqlite logs "$LAST_POD" | grep -o '"engine":"[a-z]*"' | head -1

step "2. Postgres 18 pod + helm install with database.existingSecret"
kubectl create namespace fd-pg >/dev/null
kubectl -n fd-pg apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: {name: postgres}
spec:
  replicas: 1
  selector: {matchLabels: {app: postgres}}
  template:
    metadata: {labels: {app: postgres}}
    spec:
      containers:
        - name: postgres
          image: $PG_TAG
          imagePullPolicy: Never
          args: ["-c", "shared_buffers=32MB", "-c", "max_connections=20", "-c", "work_mem=2MB"]
          env:
            - {name: POSTGRES_USER, value: st}
            - {name: POSTGRES_PASSWORD, value: fd-kind}
            - {name: POSTGRES_DB, value: featherdesk}
          ports: [{containerPort: 5432}]
          readinessProbe:
            exec: {command: ["pg_isready", "-U", "st", "-d", "featherdesk"]}
            periodSeconds: 1
          volumeMounts: [{name: data, mountPath: /var/lib/postgresql}]
      volumes: [{name: data, emptyDir: {}}]
---
apiVersion: v1
kind: Service
metadata: {name: postgres}
spec:
  selector: {app: postgres}
  ports: [{port: 5432}]
YAML
kubectl -n fd-pg rollout status deploy/postgres --timeout=90s
kubectl -n fd-pg create secret generic fd-db \
  --from-literal=FD_DATABASE_URL='postgres://st:fd-kind@postgres.fd-pg.svc:5432/featherdesk?sslmode=disable' >/dev/null
install stpg fd-pg "$PORT2" --set database.existingSecret=fd-db
kubectl -n fd-pg logs "$LAST_POD" | grep -q '"engine":"postgres"' || { kubectl -n fd-pg logs "$LAST_POD" | tail -20; fail "app log has no engine=postgres"; }
echo 'stpg: app log shows "engine":"postgres"'

step "3. static checks (helm lint, replicaCount=2 refused, kubeconform -strict)"
"$ROOT/scripts/k8s-validate.sh"

echo
echo "PASS: kind $(kind version | awk '{print $2}'), helm $(helm version --short), image $IMAGE"
