#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# AUTH-031 interop setup for the dev stack (deploy/compose.dev.yml): makes sure
# the SimpleTickets team exists, then recreates the app with
# ST_OIDC_STAFF_TEAM_MAP=<group>=<team> (an unknown team is a boot error, so
# the team must exist before the map is set).
#
#   scripts/dev-team-map.sh                 # st-tech=technical (default)
#   scripts/dev-team-map.sh st-billing billing
#
# Needs the dev stack up (app on :8080, break-glass admin from deploy/.env.dev).
set -euo pipefail

GROUP=${1:-st-tech}
TEAM=${2:-technical}
BASE=${ST_DEV_URL:-http://localhost:8080}
ADMIN_EMAIL=${E2E_ADMIN_EMAIL:-admin@dev.test}
ADMIN_PASSWORD=${E2E_ADMIN_PASSWORD:-dev-admin-password-123}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
JAR=$(mktemp)
trap 'rm -f "$JAR"' EXIT

me=$(curl -fsS -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"audience\":\"staff\",\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" "$BASE/api/v1/auth/login")
csrf=$(printf '%s' "$me" | python3 -c 'import sys,json; print(json.load(sys.stdin)["csrf_token"])')

have=$(curl -fsS -b "$JAR" "$BASE/api/v1/admin/teams" |
  python3 -c 'import sys,json; t=sys.argv[1].lower(); print(next((x["id"] for x in json.load(sys.stdin) if x["name"].lower()==t), ""))' "$TEAM")
if [ -z "$have" ]; then
  curl -fsS -b "$JAR" -H 'Content-Type: application/json' -H "X-CSRF-Token: $csrf" -H "Origin: $BASE" \
    -d "{\"name\":\"$TEAM\"}" "$BASE/api/v1/admin/teams" >/dev/null
  echo "created team $TEAM"
else
  echo "team $TEAM exists ($have)"
fi

ST_DEV_TEAM_MAP="$GROUP=$TEAM" docker compose -f "$ROOT/deploy/compose.yml" -f "$ROOT/deploy/compose.dev.yml" \
  up -d --no-deps --wait app
curl -fsS "$BASE/readyz" >/dev/null
echo "app restarted with ST_OIDC_STAFF_TEAM_MAP=$GROUP=$TEAM"
