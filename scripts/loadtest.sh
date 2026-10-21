#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-024: run tools/loadtest against the PRODUCTION compose (mem_limit 256m,
# cpus 1, read_only) with 1 000 seeded tickets. Reports peak RSS, GC share,
# REST p50/p95 and the WebSocket delivery lag p50/p95 (client clock).
#
# Usage: scripts/loadtest.sh [--image simpletickets:local] [--duration 10m]
#                            [--mem 256m] [--cpus 1] [extra loadtest flags]
# --mem/--cpus override the app's mem_limit/cpus (DEP-025 low-end host runs:
# 512m and 256m); the defaults are the values in deploy/compose.yml.
set -euo pipefail
IMAGE=${IMAGE:-simpletickets:local}
DURATION=10m
MEM=
CPUS=
EXTRA=()
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    --duration) DURATION=$2; shift 2 ;;
    --mem) MEM=$2; shift 2 ;;
    --cpus) CPUS=$2; shift 2 ;;
    *) EXTRA+=("$1"); shift ;;
  esac
done
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PROJECT=st-load-$$
PORT=${ST_LOADTEST_PORT:-18091}
case $IMAGE in
  *:*) export ST_IMAGE=${IMAGE%:*} ST_VERSION=${IMAGE##*:} ;;
  *) export ST_IMAGE=$IMAGE ST_VERSION=latest ;;
esac
METRICS_TOKEN=load-metrics-$$
cat >"$WORK/app.env" <<EOF
ST_SECRET_KEY=$(head -c 32 /dev/urandom | base64)
ST_PUBLIC_URL=http://127.0.0.1:$PORT
ST_INSECURE_COOKIES=true
ST_AUTH_STAFF_LOCAL=true
ST_METRICS_TOKEN=$METRICS_TOKEN
# Test only: lets each simulated agent log in from its own X-Forwarded-For.
ST_TRUSTED_PROXIES=0.0.0.0/0
ST_LOG_LEVEL=warn
EOF
export ST_ENV_FILE="$WORK/app.env" ST_PORT=$PORT ST_BIND=127.0.0.1
FILES=(-f "$ROOT/deploy/compose.yml")
if [ -n "$MEM$CPUS" ]; then
  { echo "services:"; echo "  app:"
    [ -n "$MEM" ] && echo "    mem_limit: $MEM"
    [ -n "$CPUS" ] && echo "    cpus: $CPUS"; } >"$WORK/limits.yml"
  FILES+=(-f "$WORK/limits.yml")
fi
dc() { docker compose -p "$PROJECT" "${FILES[@]}" "$@"; }
cleanup() {
  dc logs app 2>/dev/null | grep -iE '"level":"(ERROR|WARN)"' | tail -5 || true
  dc down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

dc run --rm --no-deps app seed --tickets 1000 >"$WORK/seed.log" 2>&1 || { cat "$WORK/seed.log"; exit 2; }
# seed fills an empty DB, so the env bootstrap admin would be skipped: add one.
echo 'load-admin-password-123' | dc run --rm -T --no-deps app create-admin --email admin@load.test --password-stdin >>"$WORK/seed.log" 2>&1 \
  || { cat "$WORK/seed.log"; exit 2; }
dc up -d --wait app >/dev/null
APP=$(dc ps -q app)
echo "== app limits: mem_limit=$(( $(docker inspect -f '{{.HostConfig.Memory}}' "$APP") / 1048576 ))MiB cpus=$(awk -v n="$(docker inspect -f '{{.HostConfig.NanoCpus}}' "$APP")" 'BEGIN{print n/1e9}') read_only=$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$APP")"
(cd "$ROOT" && go build -o "$WORK/loadtest" ./tools/loadtest)
set +e
"$WORK/loadtest" -url "http://127.0.0.1:$PORT" -metrics-token "$METRICS_TOKEN" -container "$APP" -duration "$DURATION" ${EXTRA[@]+"${EXTRA[@]}"}
rc=$?
set -e
echo "OOMKilled=$(docker inspect -f '{{.State.OOMKilled}}' "$APP") restarts=$(docker inspect -f '{{.RestartCount}}' "$APP")"
[ "$(docker inspect -f '{{.State.OOMKilled}}' "$APP")" = false ] || rc=1
exit $rc
