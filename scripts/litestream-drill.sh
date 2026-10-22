#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-020 drill: the optional Litestream overlay really recovers a lost volume.
#
#   1. prod compose + deploy/compose.litestream.yml, replica in a throwaway
#      MinIO (defined here, in the drill's own compose file — never in deploy/)
#   2. seed SEED tickets, then create 100 tickets through the REST API
#   3. wait until the replica has caught up (Litestream ships the WAL every 1 s),
#      SIGKILL every container (no graceful final sync) and DESTROY the data volume
#   4. `up` again on an empty volume: litestream-restore pulls the replica, the
#      app boots on it; the ticket count and all 100 created ids must match
#   5. `docker compose -f deploy/compose.yml config --services` is still 1 service
#
# Usage: scripts/litestream-drill.sh [--image featherdesk:local] [--seed 20] [--wait 5]
# Exit: 0 PASS, 1 FAIL, 2 setup error.
set -euo pipefail
IMAGE=${IMAGE:-featherdesk:local}
SEED=20
WAIT=5
N=100
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    --seed) SEED=$2; shift 2 ;;
    --wait) WAIT=$2; shift 2 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PROJECT=fd-litestream-$$
PORT=${FD_LITESTREAM_PORT:-18093}
BASE=http://127.0.0.1:$PORT
case $IMAGE in
  *:*) export FD_IMAGE=${IMAGE%:*} FD_VERSION=${IMAGE##*:} ;;
  *) export FD_IMAGE=$IMAGE FD_VERSION=latest ;;
esac
# Same MinIO image as deploy/compose.test.yml (test-only, never in deploy/compose*.yml).
MINIO_IMAGE=pgsty/minio@sha256:b6bfe7239bfc83fb90d31612d9704d86039dd714f7904b3f1ad68f211e602372

cat >"$WORK/app.env" <<EOF
FD_SECRET_KEY=$(head -c 32 /dev/urandom | base64)
FD_PUBLIC_URL=$BASE
FD_INSECURE_COOKIES=true
FD_AUTH_STAFF_LOCAL=true
FD_LOG_LEVEL=warn
EOF
cat >"$WORK/litestream.env" <<EOF
LITESTREAM_BUCKET=replica
LITESTREAM_PATH=featherdesk
LITESTREAM_ENDPOINT=http://minio:9000
LITESTREAM_REGION=us-east-1
LITESTREAM_FORCE_PATH_STYLE=true
LITESTREAM_ACCESS_KEY_ID=drill
LITESTREAM_SECRET_ACCESS_KEY=drill-secret-123
EOF
cat >"$WORK/minio.yml" <<EOF
services:
  minio:
    image: $MINIO_IMAGE
    command: ["server", "/data"]
    environment: { MINIO_ROOT_USER: drill, MINIO_ROOT_PASSWORD: drill-secret-123 }
    volumes: [replica:/data]
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:9000/minio/health/live"]
      interval: 2s
      retries: 30
  minio-init:
    image: $MINIO_IMAGE
    depends_on: { minio: { condition: service_healthy } }
    entrypoint: ["/bin/sh", "-c"]
    command: ["mc alias set r http://minio:9000 drill drill-secret-123 && mc mb --ignore-existing r/replica"]
    restart: "no"
volumes:
  replica:
EOF
export FD_ENV_FILE="$WORK/app.env" FD_LITESTREAM_ENV_FILE="$WORK/litestream.env" FD_PORT=$PORT FD_BIND=127.0.0.1
dc() { docker compose -p "$PROJECT" -f "$ROOT/deploy/compose.yml" -f "$ROOT/deploy/compose.litestream.yml" -f "$WORK/minio.yml" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; dc logs --no-color litestream litestream-restore 2>/dev/null | tail -20 >&2; exit 1; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

echo "== 1. default compose is one service"
n=$(docker compose -f "$ROOT/deploy/compose.yml" config --services | wc -l | tr -d ' ')
[ "$n" = 1 ] || fail "deploy/compose.yml has $n services"
echo "deploy/compose.yml services: $(docker compose -f "$ROOT/deploy/compose.yml" config --services | tr '\n' ' ')"

echo "== 2. replica store, empty volume, seed $SEED tickets"
dc up -d --wait minio >/dev/null 2>&1
dc run --rm minio-init >/dev/null 2>&1 || fail "bucket"
dc run --rm litestream-restore >"$WORK/restore0.log" 2>&1 || { cat "$WORK/restore0.log"; fail "first restore step"; }
dc run --rm --no-deps app seed --tickets "$SEED" >"$WORK/seed.log" 2>&1 || { cat "$WORK/seed.log"; exit 2; }
echo 'drill-admin-password-123' | dc run --rm -T --no-deps app create-admin --email admin@drill.test --password-stdin >>"$WORK/seed.log" 2>&1 \
  || { cat "$WORK/seed.log"; exit 2; }
dc up -d --wait app litestream >/dev/null 2>&1
VOL=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}' "$(dc ps -q app)")
echo "data volume: $VOL · litestream user: $(docker inspect -f '{{.Config.User}}' "$(dc ps -q litestream)")"

# admin session → PAT
curl -fsS -c "$WORK/jar" -H 'Content-Type: application/json' -H "Origin: $BASE" \
  -d '{"audience":"staff","email":"admin@drill.test","password":"drill-admin-password-123"}' \
  "$BASE/api/v1/auth/login" >"$WORK/me.json"
CSRF=$(json 'd["csrf_token"]' <"$WORK/me.json")
TOKEN=$(curl -fsS -b "$WORK/jar" -H 'Content-Type: application/json' -H "Origin: $BASE" -H "X-CSRF-Token: $CSRF" \
  -d '{"name":"drill","scopes":["tickets:read","tickets:write"]}' "$BASE/api/v1/me/tokens" | json 'd["token"]')
api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
count() { # every ticket the admin can list, following next_cursor
  local url="$1/api/v1/agent/tickets?limit=100" cur="" total=0 page
  while :; do
    page=$(api "$url${cur:+&cursor=$cur}")
    total=$((total + $(echo "$page" | json 'len(d["items"])')))
    cur=$(echo "$page" | json 'd.get("next_cursor") or ""')
    [ -n "$cur" ] || break
  done
  echo "$total"
}

echo "== 3. create $N tickets through the API"
: >"$WORK/ids"
for i in $(seq 1 "$N"); do
  api -X POST "$BASE/api/v1/agent/tickets" -H "Idempotency-Key: drill-$$-$i" \
    -d "{\"subject\":\"litestream drill $i\",\"body_md\":\"replicated ticket $i\",\"requester_email\":\"drill$((i % 7))@example.com\",\"requester_name\":\"Drill $i\"}" \
    | json 'd["id"]' >>"$WORK/ids"
done
BEFORE=$(count "$BASE")
echo "tickets before disaster: $BEFORE (seeded $SEED + created $(wc -l <"$WORK/ids" | tr -d ' '))"
[ "$BEFORE" -ge "$N" ] || fail "only $BEFORE tickets visible"

echo "== 4. wait ${WAIT}s for the replica, SIGKILL everything, destroy the volume"
sleep "$WAIT"
dc logs --no-color litestream 2>&1 | tail -3
dc kill -s SIGKILL app litestream >/dev/null 2>&1
dc rm -f -s app litestream litestream-restore >/dev/null 2>&1
docker volume rm "$VOL" >/dev/null
docker volume inspect "$VOL" >/dev/null 2>&1 && fail "volume $VOL still exists"
echo "volume $VOL removed"

echo "== 5. up on an empty volume: restore from the replica, then boot"
t0=$(date +%s)
dc up -d --wait app litestream >"$WORK/up2.log" 2>&1 || { cat "$WORK/up2.log"; fail "restore + boot"; }
echo "restore + boot: $(( $(date +%s) - t0 ))s"
dc logs --no-color litestream-restore 2>&1 | tail -4
AFTER=$(count "$BASE")
echo "tickets after restore: $AFTER"
[ "$AFTER" = "$BEFORE" ] || fail "count mismatch: before $BEFORE, after $AFTER"
missing=0
while read -r id; do
  api -o /dev/null "$BASE/api/v1/agent/tickets/$id" || missing=$((missing + 1))
done <"$WORK/ids"
[ "$missing" = 0 ] || fail "$missing of $N created tickets missing after restore"
echo "all $N created tickets readable after restore"
n=$(docker compose -f "$ROOT/deploy/compose.yml" config --services | wc -l | tr -d ' ')
[ "$n" = 1 ] || fail "deploy/compose.yml has $n services"
echo "PASS: litestream drill — $BEFORE tickets before, $AFTER after restore on a new volume; compose.yml still 1 service"
