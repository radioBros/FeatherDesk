#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Doc test (DEP-017, DEP-016/032 docs CI): runs the examples of
# docs/DOCUMENTATION.md against a real server, exactly as written.
#
# Every block preceded by `<!-- doctest:NAME -->` is extracted from the manual
# (never copied by hand) and executed:
#   - the curl examples of §10.2 and §10.8 (PAT auth, create, upload + reply,
#     internal note, escalate, If-Match PATCH, cursor list, customer token);
#     `curl` is wrapped with --fail-with-body, so any 4xx/5xx fails the test;
#   - the webhook signature snippets of §12.3 (Python, Node.js, Go) verify a
#     `ticket.created` delivery that the server really sent to a local
#     receiver, and must reject the same delivery with a tampered body.
# A missing marker fails the test, so a removed example is noticed.
#
#   scripts/doc-test.sh                    # builds ./cmd/featherdesk, runs it on SQLite
#   scripts/doc-test.sh --bin bin/featherdesk
#
# Needs: go (unless --bin), curl, jq, uuidgen, python3, node.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DOC=${FD_DOCTEST_DOC:-$ROOT/docs/DOCUMENTATION.md}
BIN=""
PORT=${FD_DOCTEST_PORT:-18210}
HOOK_PORT=${FD_DOCTEST_HOOK_PORT:-18211}
while [ $# -gt 0 ]; do
  case $1 in
    --bin) BIN=$2; shift 2 ;;
    *) echo "unknown arg $1" >&2; exit 2 ;;
  esac
done
for t in curl jq uuidgen python3 node; do command -v "$t" >/dev/null || { echo "doc-test needs $t" >&2; exit 2; }; done

WORK=$(mktemp -d)
PIDS=()
cleanup() { for p in ${PIDS[@]+"${PIDS[@]}"}; do kill "$p" 2>/dev/null || true; done; rm -rf "$WORK"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; [ -f "$WORK/server.log" ] && tail -20 "$WORK/server.log" >&2; exit 1; }
pass() { echo "PASS  $*"; }

# --- extract the marked blocks -------------------------------------------
mkdir -p "$WORK/blocks"
awk -v dir="$WORK/blocks" '
  /^<!-- doctest:[a-z0-9-]+ -->$/ { name = $0; sub(/^<!-- doctest:/, "", name); sub(/ -->$/, "", name); want = 1; next }
  want && /^```[a-z]+$/ { out = dir "/" name; inblock = 1; want = 0; printf "" > out; next }
  inblock && /^```$/ { inblock = 0; close(out); next }
  inblock { print >> out }
' "$DOC"
need() { [ -s "$WORK/blocks/$1" ] || fail "docs/DOCUMENTATION.md has no <!-- doctest:$1 --> block"; }
for b in api-me create-ticket upload-reply internal-note escalate if-match list-tickets customer verify-python verify-node verify-go; do need "$b"; done
echo "extracted $(find "$WORK/blocks" -type f | wc -l | tr -d ' ') blocks from docs/DOCUMENTATION.md"

# --- server ----------------------------------------------------------------
if [ -z "$BIN" ]; then
  BIN=$WORK/featherdesk
  (cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/featherdesk)
fi
FD=http://127.0.0.1:$PORT
ADMIN_EMAIL=doctest-admin@example.com ADMIN_PASSWORD=doctest-admin-password-123
export FD_LISTEN_ADDR=127.0.0.1:$PORT FD_PUBLIC_URL=$FD FD_DATA_DIR=$WORK/data \
  FD_SECRET_KEY=doctest-secret-key-0123456789abcdef0123456789abcdef \
  FD_BOOTSTRAP_ADMIN_EMAIL=$ADMIN_EMAIL FD_BOOTSTRAP_ADMIN_PASSWORD=$ADMIN_PASSWORD \
  FD_AUTH_STAFF_LOCAL=true FD_WEBHOOK_ALLOW_PRIVATE=true FD_WEBHOOK_ALLOW_INSECURE=true \
  FD_LOG_LEVEL=warn
mkdir -p "$FD_DATA_DIR"
"$BIN" serve >"$WORK/server.log" 2>&1 &
PIDS+=($!); disown
for _ in $(seq 1 100); do curl -fsS -o /dev/null "$FD/readyz" 2>/dev/null && break; sleep 0.1; done
curl -fsS -o /dev/null "$FD/readyz" || fail "server not ready"
echo "server ready on $FD"

# --- webhook receiver: stores raw body + signature header of every POST ----
mkdir -p "$WORK/hooks"
cat >"$WORK/receiver.py" <<'EOF'
import http.server, sys, os, itertools
out = sys.argv[2]; n = itertools.count()
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        i = next(n)
        open(os.path.join(out, f"{i}.body"), "wb").write(body)
        open(os.path.join(out, f"{i}.sig"), "w").write(self.headers.get("X-FeatherDesk-Signature", ""))
        open(os.path.join(out, f"{i}.event"), "w").write(self.headers.get("X-FeatherDesk-Event", ""))
        self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
EOF
python3 -I "$WORK/receiver.py" "$HOOK_PORT" "$WORK/hooks" &
PIDS+=($!); disown

# --- fixtures through the API (admin cookie session) -----------------------
JAR=$WORK/jar
me=$(curl -fsS -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"audience\":\"staff\",\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" "$FD/api/v1/auth/login")
CSRF=$(jq -r .csrf_token <<<"$me")
adm() { # method path [json]
  local a=(-sS --fail-with-body -b "$JAR" -X "$1" -H "X-CSRF-Token: $CSRF" -H "Origin: $FD")
  [ $# -ge 3 ] && a+=(-H 'Content-Type: application/json' -d "$3")
  curl "${a[@]}" "$FD/api/v1$2"
}
API_TOKEN=$(adm POST /me/tokens '{"name":"doc-test","scopes":["tickets:read","tickets:write","directory:read","directory:write","admin"]}' | jq -r .token)
[[ $API_TOKEN == fd_pat_* ]] || fail "could not create a PAT"
TECH_TEAM_ID=$(adm POST /admin/teams '{"name":"Technical","notify_mode":"agents"}' | jq -r .id)
AGENT_ID=$(adm POST /admin/staff "{\"email\":\"l3-agent@example.com\",\"name\":\"Doc Agent\",\"role\":\"agent\",\"tier\":3,\"team_ids\":[\"$TECH_TEAM_ID\"],\"password\":\"doctest-agent-password-123\"}" | jq -r .id)
TEAM_ID=$TECH_TEAM_ID
hook=$(adm POST /admin/webhooks "{\"url\":\"http://127.0.0.1:$HOOK_PORT/hook\",\"events\":[\"ticket.created\"]}")
WEBHOOK_SECRET=$(jq -r '.secret // empty' <<<"$hook")
[ -n "$WEBHOOK_SECRET" ] || fail "webhook create returned no secret: $hook"

# A customer with a password (reset-password prints a generated one), then a customer PAT.
adm POST /agent/customers '{"email":"ann@example.com","name":"Ann Example"}' >/dev/null
CUST_PW=$("$BIN" reset-password --email ann@example.com 2>/dev/null | grep -oE '[A-Za-z0-9_-]{16,}' | tail -1)
[ -n "$CUST_PW" ] || fail "reset-password printed no password"
CJAR=$WORK/cjar
cme=$(curl -fsS -c "$CJAR" -H 'Content-Type: application/json' \
  -d "{\"audience\":\"customer\",\"email\":\"ann@example.com\",\"password\":\"$CUST_PW\"}" "$FD/api/v1/auth/login")
CUSTOMER_TOKEN=$(curl -sS --fail-with-body -b "$CJAR" -H "X-CSRF-Token: $(jq -r .csrf_token <<<"$cme")" -H "Origin: $FD" \
  -H 'Content-Type: application/json' -d '{"name":"doc-test","scopes":["tickets:read","tickets:write"]}' "$FD/api/v1/me/tokens" | jq -r .token)
[[ $CUSTOMER_TOKEN == fd_pat_* ]] || fail "could not create a customer PAT"
CUSTOMER_TICKET_ID=$(curl -fsS -H "Authorization: Bearer $CUSTOMER_TOKEN" -H 'Content-Type: application/json' \
  -d '{"subject":"Earlier question","body_md":"Fixture ticket for the customer example."}' "$FD/api/v1/portal/tickets" | jq -r .id)

# --- run the curl examples --------------------------------------------------
# A `curl` earlier on PATH that adds --fail-with-body: an HTTP error fails the block.
mkdir -p "$WORK/shim"
REAL_CURL=$(command -v curl)
printf '#!/bin/sh\nexec "%s" --fail-with-body "$@"\n' "$REAL_CURL" >"$WORK/shim/curl"
chmod +x "$WORK/shim/curl"
mkdir -p "$WORK/run" && cd "$WORK/run"
printf '%%PDF-1.4\n%% doc-test attachment\n' >driver-instructions.pdf
export FD API_TOKEN TECH_TEAM_ID AGENT_ID TEAM_ID CUSTOMER_TOKEN
run_block() { # name [VAR=value…] → stdout of the block
  local name=$1; shift
  # The examples set FD/API_TOKEN to placeholders; the test provides real ones.
  grep -vE '^export (ST|API_TOKEN)=' "$WORK/blocks/$name" >"$WORK/$name.sh"
  if ! env "$@" PATH="$WORK/shim:$PATH" bash -eo pipefail "$WORK/$name.sh" >"$WORK/$name.out" 2>"$WORK/$name.err"; then
    cat "$WORK/$name.out" "$WORK/$name.err" >&2
    fail "doc example '$name' failed"
  fi
  cat "$WORK/$name.out"
}

out=$(run_block api-me)
[ "$(jq -r .email <<<"$out")" = "$ADMIN_EMAIL" ] || fail "api-me: unexpected /me: $out"
pass "§10.2 GET /me with a PAT"

out=$(run_block create-ticket)
TICKET_ID=$(jq -r .id <<<"$out")
[ "$(jq -r .status <<<"$out")" = new ] && [ -n "$TICKET_ID" ] || fail "create-ticket: $out"
export TICKET_ID
pass "§10.8 create a ticket on behalf of a customer (#$(jq -r .number <<<"$out"), status new)"

out=$(run_block upload-reply)
[ "$(jq -r '.attachments[0].filename' <<<"$out")" = driver-instructions.pdf ] || fail "upload-reply: attachment missing: $out"
st=$(curl -fsS -H "Authorization: Bearer $API_TOKEN" "$FD/api/v1/agent/tickets/$TICKET_ID" | jq -r .status)
[ "$st" = pending ] || fail "upload-reply: ticket status $st, want pending"
pass "§10.8 upload an attachment and reply with it (attachment on the message, status pending)"

run_block internal-note >/dev/null
pass "§10.8 add an internal note"

out=$(run_block escalate)
[ "$(jq -r '.ticket.tier' <<<"$out")" = 2 ] && [ "$(jq -r '.ticket.team_id' <<<"$out")" = "$TECH_TEAM_ID" ] || fail "escalate: $out"
pass "§10.8 escalate to tier 2 and the Technical team"

out=$(run_block if-match)
[ "$(jq -r '.ticket.assignee.id' <<<"$out")" = "$AGENT_ID" ] && [ "$(jq -r '.ticket.priority' <<<"$out")" = urgent ] || fail "if-match: $out"
pass "§10.8 PATCH with If-Match (assignee + priority)"

# The example ticket is pending by now; give the team one open ticket to list.
curl -fsS -H "Authorization: Bearer $API_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"subject\":\"Toner low on 3rd floor\",\"body_md\":\"Fixture.\",\"requester_email\":\"ann@example.com\",\"team_id\":\"$TECH_TEAM_ID\"}" \
  "$FD/api/v1/agent/tickets" >/dev/null
out=$(run_block list-tickets)
grep -q '"Toner low on 3rd floor"' <<<"$out" || fail "list-tickets: the team's open ticket is not listed: $out"
pass "§10.8 list open tickets of a team"

out=$(run_block customer TICKET_ID="$CUSTOMER_TICKET_ID")
[ "$(jq -sr '.[0].subject' <<<"$out")" = "Invoice 2026-114 is wrong" ] || fail "customer: $out"
pass "§10.8 customer token: open a ticket and reply"

# --- webhook signature snippets ---------------------------------------------
for _ in $(seq 1 100); do
  f=$(grep -l '^ticket.created$' "$WORK"/hooks/*.event 2>/dev/null | head -1 || true)
  [ -n "$f" ] && break; sleep 0.1
done
[ -n "${f:-}" ] || fail "no ticket.created delivery reached the receiver"
D=${f%.event}
cp "$D.body" "$WORK/body"; cp "$D.sig" "$WORK/sig"
printf 'tampered' >>"$WORK/tampered"; cat "$D.body" >>"$WORK/tampered"
echo "captured delivery: $(wc -c <"$WORK/body" | tr -d ' ') bytes, signature $(cut -c1-24 "$WORK/sig")…"

cp "$WORK/blocks/verify-python" "$WORK/verify_doc.py"
cat >"$WORK/run_py.py" <<'EOF'
import sys
sys.path.insert(0, sys.argv[1])
from verify_doc import verify
secret, sig = sys.argv[2], open(sys.argv[3]).read()
ok = verify(secret, sig, open(sys.argv[4], "rb").read())
bad = verify(secret, sig, open(sys.argv[5], "rb").read())
print(ok, bad); sys.exit(0 if ok and not bad else 1)
EOF
python3 -I "$WORK/run_py.py" "$WORK" "$WEBHOOK_SECRET" "$WORK/sig" "$WORK/body" "$WORK/tampered" >/dev/null || fail "Python snippet did not verify the real signature"
pass "§12.3 Python snippet verifies the real signature and rejects a tampered body"

cp "$WORK/blocks/verify-node" "$WORK/verify_doc.mjs"
cat >"$WORK/run_node.mjs" <<'EOF'
import { readFileSync } from 'node:fs';
const { verify } = await import(process.argv[2]);
const [secret, sigFile, body, tampered] = process.argv.slice(3);
const sig = readFileSync(sigFile, 'utf8');
const ok = verify(secret, sig, readFileSync(body)), bad = verify(secret, sig, readFileSync(tampered));
process.exit(ok && !bad ? 0 : 1);
EOF
node "$WORK/run_node.mjs" "$WORK/verify_doc.mjs" "$WEBHOOK_SECRET" "$WORK/sig" "$WORK/body" "$WORK/tampered" || fail "Node.js snippet did not verify the real signature"
pass "§12.3 Node.js snippet verifies the real signature and rejects a tampered body"

mkdir -p "$WORK/gov"
{
  printf 'package main\n\nimport (\n\t"crypto/hmac"\n\t"crypto/sha256"\n\t"encoding/hex"\n\t"math"\n\t"os"\n\t"strconv"\n\t"strings"\n\t"time"\n)\n\n'
  cat "$WORK/blocks/verify-go"
  printf '\nfunc main() {\n\tsig, _ := os.ReadFile(os.Args[2])\n\tbody, _ := os.ReadFile(os.Args[3])\n\tbad, _ := os.ReadFile(os.Args[4])\n\tif !Verify(os.Args[1], string(sig), body, time.Now()) || Verify(os.Args[1], string(sig), bad, time.Now()) {\n\t\tos.Exit(1)\n\t}\n}\n'
} >"$WORK/gov/main.go"
(cd "$WORK/gov" && GOFLAGS=-mod=mod GO111MODULE=off go run main.go "$WEBHOOK_SECRET" "$WORK/sig" "$WORK/body" "$WORK/tampered") || fail "Go snippet did not verify the real signature"
pass "§12.3 Go snippet verifies the real signature and rejects a tampered body"

echo "ok: 8 curl examples and 3 signature snippets from docs/DOCUMENTATION.md passed against a live server"
