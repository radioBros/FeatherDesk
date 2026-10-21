#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Ops doc test (DEP-032): runs the operator commands of docs/DOCUMENTATION.md
# against real stacks. Every block preceded by `<!-- doctest:NAME -->` is
# extracted from the manual (never copied by hand) and executed:
#
#   1 pg-overlay      §2.3  `docker compose … compose.postgres.yml up -d`, verbatim;
#                           /readyz must say `ready postgres`.
#   2 migrate-engine  §2.3  `simpletickets migrate-engine --from sqlite:///data/… --to
#                           postgres://simpletickets:secret@db:5432/…`, verbatim, through
#                           a `simpletickets` function = the image on a network where
#                           `db` is a Postgres 18 container; after the doc's step 2
#                           (`simpletickets migrate` with ST_DATABASE_URL). The app then
#                           boots on Postgres with every seeded ticket.
#   3 blob-migrate    §2.4  against MinIO (deploy/compose.test.yml). The ONLY rewrite:
#                           the placeholder s3:// URL becomes the MinIO bucket URL.
#                           Then the local copies are deleted and the attachment is
#                           served from S3 with the same SHA-256.
#   4 k8s-plain       §2.5  plain manifests on kind; helm-values + helm-install +
#     helm-*                helm-token with the Helm chart: rollout ready, /readyz 200,
#                           the log shows the setup token.
#   5 imap-env        §6.4  the documented ST_IMAP_* environment mailbox against
#                           Dovecot (compose.test.yml); only the values that point at
#                           the test server are replaced (host, port, security,
#                           user, password). A message APPENDed to INBOX must become
#                           a ticket.
#
#   scripts/doc-test-ops.sh [--image simpletickets:local] [--steps 1,2,3,4,5]
#
# Needs: docker, curl, jq, python3; kind, helm, kubectl for step 4.
# Host ports: 18401-18406, plus 19000/19001 and 31143/31144/31993 from compose.test.yml.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DOC=${ST_DOCTEST_DOC:-$ROOT/docs/DOCUMENTATION.md}
IMAGE=simpletickets:local
STEPS=1,2,3,4,5
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    --steps) STEPS=$2; shift 2 ;;
    *) echo "usage: $0 [--image repo:tag] [--steps 1,2,3,4,5]" >&2; exit 2 ;;
  esac
done
for t in docker curl jq python3; do command -v "$t" >/dev/null || { echo "needs $t" >&2; exit 2; }; done
want() { [[ ",$STEPS," == *",$1,"* ]]; }

PG_IMAGE=postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
MINIO_IMAGE=pgsty/minio@sha256:b6bfe7239bfc83fb90d31612d9704d86039dd714f7904b3f1ad68f211e602372
ID=st-docops-$$
SECRET=docops-secret-key-0123456789abcdef0123456789abcdef
ADMIN=admin@docops.test
ADMIN_PW=docops-admin-password-123
WORK=$(mktemp -d)
KUBECONFIG=$WORK/kubeconfig
export KUBECONFIG

PIDS=() CONTAINERS=() VOLUMES=() NETWORKS=() PROJECTS=() CLUSTER="" GHCR_OLD=""
cleanup() {
  set +e
  for p in ${PIDS[@]+"${PIDS[@]}"}; do kill "$p" 2>/dev/null; done
  for c in ${CONTAINERS[@]+"${CONTAINERS[@]}"}; do docker rm -f "$c" >/dev/null 2>&1; done
  for p in ${PROJECTS[@]+"${PROJECTS[@]}"}; do # "project|compose file|compose file…"
    IFS='|' read -r -a parts <<<"$p"
    args=(); for f in "${parts[@]:1}"; do args+=(-f "$f"); done
    ST_PG_PASSWORD=${ST_PG_PASSWORD:-x} ST_ENV_FILE=${ST_ENV_FILE:-/dev/null} docker compose -p "${parts[0]}" "${args[@]}" down -v --remove-orphans >/dev/null 2>&1
  done
  for n in ${NETWORKS[@]+"${NETWORKS[@]}"}; do docker network rm "$n" >/dev/null 2>&1; done
  for v in ${VOLUMES[@]+"${VOLUMES[@]}"}; do docker volume rm -f "$v" >/dev/null 2>&1; done
  [ -n "$CLUSTER" ] && kind delete cluster --name "$CLUSTER" >/dev/null 2>&1
  # Step 4 tags the image as ghcr.io/radiobros/simpletickets:latest; put back what was there.
  if [ -n "$CLUSTER" ]; then
    if [ -n "$GHCR_OLD" ]; then docker tag "$GHCR_OLD" ghcr.io/radiobros/simpletickets:latest >/dev/null 2>&1
    else docker rmi ghcr.io/radiobros/simpletickets:latest >/dev/null 2>&1; fi
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT
step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS  $*"; }
docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "image $IMAGE not found (build it first)"

# --- extract the marked blocks (markers and fences may be indented in lists) --
mkdir -p "$WORK/blocks"
awk -v dir="$WORK/blocks" '
  /^[ ]*<!-- doctest:[a-z0-9-]+ -->$/ { name = $0; sub(/^[ ]*<!-- doctest:/, "", name); sub(/ -->$/, "", name); want = 1; next }
  want && /^[ ]*```[a-z]+$/ { match($0, /^[ ]*/); ind = RLENGTH; out = dir "/" name; inblock = 1; want = 0; printf "" > out; next }
  inblock && /^[ ]*```$/ { inblock = 0; close(out); next }
  inblock { print substr($0, ind + 1) >> out }
' "$DOC"
block() { [ -s "$WORK/blocks/$1" ] || fail "docs/DOCUMENTATION.md has no <!-- doctest:$1 --> block"; echo "$WORK/blocks/$1"; }
# Run a doc block with bash -euo pipefail in a directory; the caller provides env/functions via BASH_ENV.
run_block() { # name dir [BASH_ENV file]
  local f; f=$(block "$1")
  ( cd "$2" && BASH_ENV=${3:-} bash -euo pipefail "$f" ) >"$WORK/$1.out" 2>&1 || { cat "$WORK/$1.out" >&2; fail "doc block '$1' failed"; }
}
wait_ready() { # url want-substring seconds
  local i body=""
  for ((i = 0; i < $3 * 5; i++)); do
    body=$(curl -fsS --max-time 2 "$1/readyz" 2>/dev/null || true)
    [[ $body == *"$2"* ]] && { echo "$body"; return 0; }
    sleep 0.2
  done
  echo "last /readyz: '$body'" >&2; return 1
}
admin_login() { # base jar → csrf
  curl -fsS -c "$2" -H 'Content-Type: application/json' \
    -d "{\"audience\":\"staff\",\"email\":\"$ADMIN\",\"password\":\"$ADMIN_PW\"}" "$1/api/v1/auth/login" | jq -r .csrf_token
}
pat() { # base → admin PAT
  local jar=$WORK/jar-$RANDOM csrf
  csrf=$(admin_login "$1" "$jar")
  curl -fsS -b "$jar" -H "X-CSRF-Token: $csrf" -H "Origin: $1" -H 'Content-Type: application/json' \
    -d '{"name":"doc-test-ops","scopes":["tickets:read","tickets:write","directory:read","directory:write","admin"]}' \
    "$1/api/v1/me/tokens" | jq -r .token
}
# The image as the doc's `simpletickets` command (DOCOPS_NET / DOCOPS_VOL / DOCOPS_ENV select the context).
cat >"$WORK/st-fn.sh" <<EOF
simpletickets() {
  local envargs=(-e ST_SECRET_KEY=$SECRET)
  [ -n "\${ST_DATABASE_URL:-}" ] && envargs+=(-e ST_DATABASE_URL="\$ST_DATABASE_URL")
  for kv in \${DOCOPS_ENV:-}; do envargs+=(-e "\$kv"); done
  docker run --rm --network "\$DOCOPS_NET" -v "\$DOCOPS_VOL:/data" "\${envargs[@]}" "$IMAGE" "\$@"
}
EOF

# ============================================================================
if want 1; then
  step "1. §2.3 Postgres overlay (compose.yml + compose.postgres.yml)"
  P=$ID-pg PORT=18401
  printf 'ST_SECRET_KEY=%s\nST_PUBLIC_URL=http://127.0.0.1:%s\n' "$SECRET" "$PORT" >"$WORK/pg.env"
  PROJECTS+=("$P|$ROOT/deploy/compose.yml|$ROOT/deploy/compose.postgres.yml")
  t0=$(date +%s)
  # The compose files read these from the environment (deploy/.env in a real install).
  export COMPOSE_PROJECT_NAME=$P ST_ENV_FILE=$WORK/pg.env ST_PORT=$PORT ST_BIND=127.0.0.1 ST_PG_PASSWORD=docops-pg-$$
  export ST_IMAGE=${IMAGE%:*} ST_VERSION=${IMAGE##*:}
  run_block pg-overlay "$ROOT"
  body=$(wait_ready "http://127.0.0.1:$PORT" postgres 90) || fail "overlay: /readyz never reported postgres"
  pass "§2.3 overlay up verbatim: /readyz '$body' $(( $(date +%s) - t0 )) s after up; services: $(docker compose -p "$P" -f "$ROOT/deploy/compose.yml" -f "$ROOT/deploy/compose.postgres.yml" ps --services | sort | tr '\n' ' ')"
  docker compose -p "$P" -f "$ROOT/deploy/compose.yml" -f "$ROOT/deploy/compose.postgres.yml" down -v >/dev/null 2>&1
  unset COMPOSE_PROJECT_NAME
fi

# ============================================================================
if want 2; then
  step "2. §2.3 migrate-engine SQLite → Postgres"
  NET=$ID-net VOL=$ID-data DB=$ID-db PORT=18402
  docker network create "$NET" >/dev/null; NETWORKS+=("$NET")
  docker volume create "$VOL" >/dev/null; VOLUMES+=("$VOL")
  docker run -d --name "$DB" --network "$NET" --network-alias db -e POSTGRES_USER=simpletickets \
    -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=simpletickets "$PG_IMAGE" >/dev/null; CONTAINERS+=("$DB")
  for _ in $(seq 1 60); do docker exec "$DB" pg_isready -U simpletickets -d simpletickets >/dev/null 2>&1 && break; sleep 0.5; done
  export DOCOPS_NET=$NET DOCOPS_VOL=$VOL
  # shellcheck source=/dev/null
  . "$WORK/st-fn.sh"
  simpletickets seed --tickets 25 >"$WORK/seed.out" 2>&1 || { cat "$WORK/seed.out"; fail "seed"; }
  # Doc step 2: bring the empty Postgres database to the current schema.
  ST_DATABASE_URL='postgres://simpletickets:secret@db:5432/simpletickets?sslmode=disable' simpletickets migrate >"$WORK/migrate.out" 2>&1 \
    || { cat "$WORK/migrate.out"; fail "simpletickets migrate on Postgres"; }
  t0=$(date +%s)
  run_block migrate-engine "$WORK" "$WORK/st-fn.sh"
  echo "migrate-engine ($(( $(date +%s) - t0 )) s): $(grep -oE 'Table:(users|tickets|ticket_messages) Rows:[0-9]+|NeedsReindex:[a-z]+' "$WORK/migrate-engine.out" | tr '\n' ' ')"
  # The doc: "If the report says the search index needs rebuilding, run simpletickets reindex."
  if grep -q 'NeedsReindex:true' "$WORK/migrate-engine.out"; then
    ST_DATABASE_URL='postgres://simpletickets:secret@db:5432/simpletickets?sslmode=disable' simpletickets reindex >"$WORK/reindex.out" 2>&1 \
      || { cat "$WORK/reindex.out"; fail "simpletickets reindex"; }
    echo "reindex: $(tail -1 "$WORK/reindex.out")"
  fi
  pg_tickets=$(docker exec "$DB" psql -U simpletickets -d simpletickets -tAc 'SELECT count(*) FROM tickets')
  pg_users=$(docker exec "$DB" psql -U simpletickets -d simpletickets -tAc 'SELECT count(*) FROM users')
  [ "$pg_tickets" = 25 ] || fail "Postgres has $pg_tickets tickets after migrate-engine, want 25 (seeded)"
  # Doc step 4: start the app on the Postgres URL.
  docker run -d --name "$ID-app2" --network "$NET" -v "$VOL:/data" -p "127.0.0.1:$PORT:8080" -e ST_SECRET_KEY=$SECRET \
    -e ST_PUBLIC_URL="http://127.0.0.1:$PORT" -e ST_DATABASE_URL='postgres://simpletickets:secret@db:5432/simpletickets?sslmode=disable' \
    --read-only --tmpfs /tmp "$IMAGE" serve >/dev/null; CONTAINERS+=("$ID-app2")
  body=$(wait_ready "http://127.0.0.1:$PORT" postgres 60) || { docker logs "$ID-app2" | tail -20; fail "app on Postgres not ready"; }
  pass "§2.3 migrate-engine verbatim: 25/25 seeded tickets and $pg_users users in Postgres; the app boots on it ('$body')"
  docker rm -f "$ID-app2" "$DB" >/dev/null
fi

# ============================================================================
TESTP=$ID-test
if want 3 || want 5; then
  step "MinIO + Dovecot (deploy/compose.test.yml)"
  PROJECTS+=("$TESTP|$ROOT/deploy/compose.test.yml")
  docker compose -p "$TESTP" -f "$ROOT/deploy/compose.test.yml" up -d --wait minio dovecot >/dev/null 2>&1 \
    || { docker compose -p "$TESTP" -f "$ROOT/deploy/compose.test.yml" ps -a; fail "compose.test.yml up"; }
  docker compose -p "$TESTP" -f "$ROOT/deploy/compose.test.yml" up minio-init >/dev/null 2>&1 || fail "minio-init (bucket test)"
fi
TNET=${TESTP}_default

if want 3; then
  step "3. §2.4 blob-migrate to S3 (MinIO)"
  VOL=$ID-blob PORT=18403 BASE=http://127.0.0.1:18403
  docker volume create "$VOL" >/dev/null; VOLUMES+=("$VOL")
  APPENV=(-e "ST_SECRET_KEY=$SECRET" -e "ST_PUBLIC_URL=$BASE" -e "ST_BOOTSTRAP_ADMIN_EMAIL=$ADMIN" -e "ST_BOOTSTRAP_ADMIN_PASSWORD=$ADMIN_PW" -e ST_AUTH_STAFF_LOCAL=true)
  S3URL='s3://test/simpletickets?endpoint=http://minio:9000&region=us-east-1&path_style=true'
  S3KEYS=(-e ST_S3_ACCESS_KEY=st-test -e ST_S3_SECRET_KEY=st-test-secret-123)
  docker run -d --name "$ID-app3" --network "$TNET" -v "$VOL:/data" -p "127.0.0.1:$PORT:8080" "${APPENV[@]}" "$IMAGE" serve >/dev/null; CONTAINERS+=("$ID-app3")
  wait_ready "$BASE" ready 60 >/dev/null || fail "app (local blobs) not ready"
  TOKEN=$(pat "$BASE")
  head -c 70000 /dev/urandom >"$WORK/blob.bin"
  want_sha=$(shasum -a 256 "$WORK/blob.bin" | cut -d' ' -f1)
  tid=$(curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"subject":"S3 doc test","body_md":"x","requester_email":"s3@example.com","requester_name":"S3"}' "$BASE/api/v1/agent/tickets" | jq -r .id)
  att=$(curl -fsS -H "Authorization: Bearer $TOKEN" -F "file=@$WORK/blob.bin;filename=blob.bin" "$BASE/api/v1/agent/attachments" | jq -r .id)
  curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d "{\"kind\":\"public\",\"body_md\":\"file\",\"attachment_ids\":[\"$att\"]}" "$BASE/api/v1/agent/tickets/$tid/messages" >/dev/null
  docker stop "$ID-app3" >/dev/null
  # The doc block with ONLY its placeholder s3:// URL replaced by the MinIO bucket.
  sed -E "s#s3://my-bucket/simpletickets\?endpoint=[^']*#${S3URL//&/\\&}#" "$(block blob-migrate)" >"$WORK/blocks/blob-migrate.minio"
  grep -qF "$S3URL" "$WORK/blocks/blob-migrate.minio" || fail "blob-migrate block: placeholder URL not found"
  cp "$WORK/blocks/blob-migrate.minio" "$WORK/blocks/blob-migrate"
  export DOCOPS_NET=$TNET DOCOPS_VOL=$VOL DOCOPS_ENV="ST_S3_ACCESS_KEY=st-test ST_S3_SECRET_KEY=st-test-secret-123"
  run_block blob-migrate "$WORK" "$WORK/st-fn.sh"
  out=$(cat "$WORK/blob-migrate.out")
  n=$(grep -oE 'copied [0-9]+ blobs' <<<"$out" | grep -oE '[0-9]+' || true)
  [ -n "$n" ] && [ "$n" -gt 0 ] || fail "blob-migrate: $out"
  objects=$(docker run --rm --network "$TNET" --entrypoint sh "$MINIO_IMAGE" -c \
    'mc alias set t http://minio:9000 st-test st-test-secret-123 >/dev/null && mc ls --recursive t/test/simpletickets | wc -l' | tr -d ' ')
  # Remove the local copies: from now on the attachment can only come from S3.
  docker run --rm -v "$VOL:/data" --entrypoint sh "$MINIO_IMAGE" -c 'rm -rf /data/attachments /data/branding /data/kb /data/inbound'
  docker rm -f "$ID-app3" >/dev/null
  docker run -d --name "$ID-app3b" --network "$TNET" -v "$VOL:/data" -p "127.0.0.1:$PORT:8080" "${APPENV[@]}" "${S3KEYS[@]}" \
    -e ST_BLOB_URL="$S3URL" "$IMAGE" serve >/dev/null; CONTAINERS+=("$ID-app3b")
  wait_ready "$BASE" ready 60 >/dev/null || fail "app on S3 not ready"
  got_sha=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/agent/attachments/$att" | shasum -a 256 | cut -d' ' -f1)
  [ "$got_sha" = "$want_sha" ] || fail "attachment from S3: sha256 $got_sha, want $want_sha"
  storage=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/admin/storage" | jq -c '.backend // .' | cut -c1-80)
  pass "§2.4 blob-migrate: '$(grep -oE 'copied [0-9]+ blobs \([0-9]+ bytes\)' <<<"$out")', $objects objects in the bucket; local copies deleted; attachment served from S3 with the same sha256 (${want_sha:0:12}…); storage: $storage"
  docker rm -f "$ID-app3b" >/dev/null
  unset DOCOPS_ENV
fi

# ============================================================================
if want 5; then
  step "5. §6.4 environment IMAP mailbox (Dovecot)"
  PORT=18406 BASE=http://127.0.0.1:18406
  f=$(block imap-env)
  # Every key of the doc example is a real, documented ST_IMAP_* variable.
  while IFS='=' read -r k _; do
    [ -z "$k" ] && continue
    grep -qE "^#? ?$k=" "$ROOT/.env.example" || fail "imap-env: $k is not in .env.example"
  done <"$f"
  # Point the example at the test server; every other line is used as written.
  sed -E 's/^ST_IMAP_HOST=.*/ST_IMAP_HOST=dovecot/; s/^ST_IMAP_PORT=.*/ST_IMAP_PORT=31143/; s/^ST_IMAP_SECURITY=.*/ST_IMAP_SECURITY=none/;
          s/^ST_IMAP_USERNAME=.*/ST_IMAP_USERNAME=support@test.local/; s/^ST_IMAP_PASSWORD=.*/ST_IMAP_PASSWORD=st-test-imap/' "$f" >"$WORK/imap.env"
  printf 'ST_SECRET_KEY=%s\nST_PUBLIC_URL=%s\nST_BOOTSTRAP_ADMIN_EMAIL=%s\nST_BOOTSTRAP_ADMIN_PASSWORD=%s\nST_AUTH_STAFF_LOCAL=true\n' \
    "$SECRET" "$BASE" "$ADMIN" "$ADMIN_PW" >>"$WORK/imap.env"
  docker run -d --name "$ID-app5" --network "$TNET" -p "127.0.0.1:$PORT:8080" --env-file "$WORK/imap.env" --read-only --tmpfs /tmp \
    "$IMAGE" serve >/dev/null; CONTAINERS+=("$ID-app5")
  wait_ready "$BASE" ready 60 >/dev/null || fail "app (IMAP env) not ready"
  TOKEN=$(pat "$BASE")
  mbx=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/admin/inbound/mailboxes" | jq -c '(if type == "array" then . else .items end) | map({name, kind, address})')
  # A known customer, so the message is not quarantined as unknown_sender.
  curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"email":"imap-customer@example.org","name":"IMAP Customer"}' "$BASE/api/v1/agent/customers" >/dev/null
  SUBJ="Doc test via IMAP $$"
  t0=$(python3 -c 'import time; print(time.time())')
  python3 -I - "$SUBJ" <<'EOF'
import imaplib, sys, time, email.utils
subj = sys.argv[1]
msg = ("From: IMAP Customer <imap-customer@example.org>\r\nTo: support@example.com\r\n"
       f"Subject: {subj}\r\nMessage-ID: <{time.time_ns()}@example.org>\r\nDate: {email.utils.formatdate()}\r\n"
       "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nThe printer is on fire.\r\n").encode()
c = imaplib.IMAP4("127.0.0.1", 31143)
c.login("support@test.local", "st-test-imap")
typ, _ = c.append("INBOX", None, imaplib.Time2Internaldate(time.time()), msg)
assert typ == "OK", typ
c.logout()
EOF
  found=""
  for _ in $(seq 1 150); do
    found=$(curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/agent/tickets?limit=50" | jq -r --arg s "$SUBJ" '.items[] | select(.subject == $s) | .number' | head -1)
    [ -n "$found" ] && break; sleep 0.2
  done
  dt=$(python3 -c "import time; print(f'{time.time() - $t0:.1f}')")
  [ -n "$found" ] || { curl -fsS -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/admin/inbound/messages?limit=5" >&2 || true; docker logs "$ID-app5" 2>&1 | tail -20 >&2; fail "no ticket from the IMAP message"; }
  pass "§6.4 env mailbox from the doc's ST_IMAP_* block ($mbx): APPENDed message became ticket #$found after ${dt} s"
  docker rm -f "$ID-app5" >/dev/null
fi
if want 3 || want 5; then
  docker compose -p "$TESTP" -f "$ROOT/deploy/compose.test.yml" down -v >/dev/null 2>&1
fi

# ============================================================================
if want 4; then
  step "4. §2.5 Kubernetes: plain manifests, then the Helm chart (kind)"
  for t in kind helm kubectl; do command -v "$t" >/dev/null || fail "step 4 needs $t"; done
  GHCR_OLD=$(docker image inspect -f '{{.Id}}' ghcr.io/radiobros/simpletickets:latest 2>/dev/null || true)
  CLUSTER=$ID
  kind create cluster --name "$CLUSTER" --wait 120s >/dev/null 2>&1 || fail "kind create cluster"
  # The manifests and the chart (appVersion latest, IfNotPresent) name this image.
  docker tag "$IMAGE" ghcr.io/radiobros/simpletickets:latest
  kind load docker-image --name "$CLUSTER" ghcr.io/radiobros/simpletickets:latest >/dev/null
  pf() { # resource port → background port-forward
    kubectl -n simpletickets port-forward "$1" "$2:8080" >"$WORK/pf-$2.log" 2>&1 &
    PIDS+=($!); disown "$!" 2>/dev/null || true
  }

  t0=$(date +%s)
  run_block k8s-plain "$ROOT"
  kubectl -n simpletickets rollout status deploy/simpletickets --timeout=120s >/dev/null \
    || { kubectl -n simpletickets describe pod | tail -30; fail "plain manifests: rollout"; }
  ready_s=$(( $(date +%s) - t0 ))
  pf deploy/simpletickets 18404
  wait_ready http://127.0.0.1:18404 ready 30 >/dev/null || fail "plain manifests: /readyz through port-forward"
  tok=$(kubectl -n simpletickets logs deploy/simpletickets | grep -c "setup token" || true)
  [ "$tok" -ge 1 ] || fail "plain manifests: no setup token in the log"
  pass "§2.5 plain manifests verbatim: rollout ready ${ready_s} s after apply, /readyz 200, setup token in the log"
  kill "${PIDS[${#PIDS[@]}-1]}" 2>/dev/null || true
  kubectl delete namespace simpletickets --wait=true --timeout=120s >/dev/null

  mkdir -p "$WORK/helm" && ln -s "$ROOT/deploy" "$WORK/helm/deploy"
  cp "$(block helm-values)" "$WORK/helm/my-values.yaml"
  # The values name an existing Secret holding ST_SECRET_KEY: create it as the doc says.
  ex=$(awk '/existingSecret:/ && !/^#/ {print $2; exit}' "$WORK/helm/my-values.yaml")
  kubectl create namespace simpletickets >/dev/null
  kubectl -n simpletickets create secret generic "$ex" --from-literal=ST_SECRET_KEY="$(openssl rand -base64 32)" >/dev/null
  t0=$(date +%s)
  run_block helm-install "$WORK/helm"
  run_block helm-token "$WORK/helm"
  ready_s=$(( $(date +%s) - t0 ))
  grep -q "setup token" "$WORK/helm-token.out" || fail "helm: no setup token line"
  pf deploy/simpletickets 18405
  wait_ready http://127.0.0.1:18405 ready 30 >/dev/null || fail "helm: /readyz through port-forward"
  ing=$(kubectl -n simpletickets get ingress -o jsonpath='{.items[0].spec.rules[0].host}')
  pass "§2.5 Helm verbatim (my-values.yaml from the doc, Secret '$ex'): ready + setup token ${ready_s} s after install, /readyz 200, ingress host $ing"
fi

echo
echo "ok: doc-test-ops steps $STEPS passed"
