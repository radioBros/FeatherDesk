#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-003 smoke test on the PRODUCTION compose (read_only, cap_drop ALL,
# mem_limit 256m, cpus 1): healthy, /readyz 200, exactly one service, then a
# ticket loop with an attachment upload and download, a reply that references
# the attachment, a status change and a live WebSocket event. Fails on any
# error-level log line or an OOM kill.
#
# With --engine postgres the compose.postgres.yml overlay is added (DEP-029):
# both containers healthy within 60 s and the app must log engine=postgres.
#
# Usage: scripts/compose-smoke.sh [--image simpletickets:local] [--engine sqlite|postgres]
set -euo pipefail
IMAGE=${IMAGE:-simpletickets:local}
ENGINE=sqlite
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    --engine) ENGINE=$2; shift 2 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done
ROOT=$(cd "$(dirname "$0")/.." && pwd)
FILES=(-f "$ROOT/deploy/compose.yml")
[ "$ENGINE" = postgres ] && FILES+=(-f "$ROOT/deploy/compose.postgres.yml")
WORK=$(mktemp -d)
PROJECT=st-smoke-$$
PORT=${ST_SMOKE_PORT:-18094}
BASE=http://127.0.0.1:$PORT
case $IMAGE in
  *:*) export ST_IMAGE=${IMAGE%:*} ST_VERSION=${IMAGE##*:} ;;
  *) export ST_IMAGE=$IMAGE ST_VERSION=latest ;;
esac
cat >"$WORK/app.env" <<EOF
ST_SECRET_KEY=$(head -c 32 /dev/urandom | base64)
ST_PUBLIC_URL=$BASE
ST_INSECURE_COOKIES=true
EOF
export ST_ENV_FILE="$WORK/app.env" ST_PORT=$PORT ST_BIND=127.0.0.1 ST_PG_PASSWORD=smoke-$$
dc() { docker compose -p "$PROJECT" "${FILES[@]}" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT
step() { printf '== %s\n' "$*"; }

step "exactly one service in deploy/compose.yml"
n=$(docker compose -p "$PROJECT" -f "$ROOT/deploy/compose.yml" config --services | wc -l | tr -d ' ')
[ "$n" = 1 ] || { echo "compose.yml has $n services"; exit 1; }

if [ "$ENGINE" = postgres ]; then
  step "postgres healthy"
  dc up -d --wait --wait-timeout 60 postgres >/dev/null
fi
step "seed + admin ($ENGINE)"
dc run --rm --no-deps app seed --tickets 20 >"$WORK/log" 2>&1 || { cat "$WORK/log"; exit 1; }
echo 'smoke-admin-password-123' | dc run --rm -T --no-deps app create-admin --email admin@smoke.test --password-stdin >>"$WORK/log" 2>&1 \
  || { cat "$WORK/log"; exit 1; }

step "up, healthy within 30 s"
t0=$(date +%s)
dc up -d --wait --wait-timeout 30 app >/dev/null
echo "healthy after $(( $(date +%s) - t0 ))s"
engine=$(dc logs app 2>&1 | grep -m1 '"msg":"serving"' | grep -oE '"engine":"[a-z]+"' || true)
echo "app serving with $engine"
[ "$engine" = "\"engine\":\"$ENGINE\"" ] || { echo "expected engine=$ENGINE"; exit 1; }
curl -fsS "$BASE/readyz" >/dev/null
APP=$(dc ps -q app)

step "login, PAT"
me=$(curl -fsS -c "$WORK/jar" -H 'Content-Type: application/json' -H "Origin: $BASE" \
  --data '{"audience":"staff","email":"admin@smoke.test","password":"smoke-admin-password-123"}' "$BASE/api/v1/auth/login")
csrf=$(printf '%s' "$me" | sed -E 's/.*"csrf_token":"([^"]+)".*/\1/')
pat=$(curl -fsS -b "$WORK/jar" -H 'Content-Type: application/json' -H "Origin: $BASE" -H "X-CSRF-Token: $csrf" \
  --data '{"name":"smoke","scopes":["tickets:read","tickets:write"]}' "$BASE/api/v1/me/tokens" | sed -E 's/.*"token":"([^"]+)".*/\1/')
H=(-H "Authorization: Bearer $pat")

step "ticket loop with attachment (writes must go to /data or /tmp)"
tid=$(curl -fsS "${H[@]}" "$BASE/api/v1/agent/tickets?limit=1" | sed -E 's/.*"items":\[\{"id":"([^"]+)".*/\1/')
head -c 300000 /dev/urandom >"$WORK/blob.bin"
att=$(curl -fsS "${H[@]}" -F "file=@$WORK/blob.bin;type=application/octet-stream" "$BASE/api/v1/agent/attachments" | sed -E 's/.*"id":"([^"]+)".*/\1/')
curl -fsS "${H[@]}" -H 'Content-Type: application/json' \
  --data "{\"kind\":\"public\",\"body_md\":\"smoke reply with attachment\",\"attachment_ids\":[\"$att\"]}" \
  "$BASE/api/v1/agent/tickets/$tid/messages" >/dev/null
curl -fsS "${H[@]}" -o "$WORK/back.bin" "$BASE/api/v1/agent/attachments/$att"
cmp -s "$WORK/blob.bin" "$WORK/back.bin" || { echo "attachment round trip differs"; exit 1; }
curl -fsS "${H[@]}" -X PATCH -H 'Content-Type: application/json' --data '{"status":"pending"}' "$BASE/api/v1/agent/tickets/$tid" >/dev/null
echo "ticket $tid: attachment $att (300 KB) uploaded, attached to a reply, downloaded identical; status → pending"

step "WebSocket live update"
(cd "$ROOT" && go build -o "$WORK/loadtest" ./tools/loadtest)
"$WORK/loadtest" -probe -url "$BASE" -admin-email admin@smoke.test -admin-password smoke-admin-password-123

step "logs and limits"
if dc logs app 2>&1 | grep -E '"level":"ERROR"' | grep -vE '"status":4[0-9][0-9]'; then
  echo "error-level log lines (read-only filesystem?)"; exit 1
fi
oom=$(docker inspect -f '{{.State.OOMKilled}}' "$APP"); rc=$(docker inspect -f '{{.RestartCount}}' "$APP")
mem=$(docker inspect -f '{{.HostConfig.Memory}}' "$APP"); ro=$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$APP")
echo "OOMKilled=$oom restarts=$rc mem_limit=$((mem/1048576))MiB read_only=$ro"
[ "$oom" = false ] && [ "$rc" = 0 ] && [ "$ro" = true ]
echo "smoke: PASS"
