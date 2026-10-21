#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# NOT-023 + NOT-026 end to end against a running instance (the dev stack by
# default): creates a customer and a webhook mailbox through the admin API,
# POSTs a raw RFC 5322 message with curl to /api/v1/inbound/mime/{token}
# (expects 202), waits until the inbound log shows it processed into a ticket,
# then downloads the ticket's original.eml attachment and checks that its
# sha256 equals the sha256 of the bytes that were posted.
#
#   docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build
#   scripts/inbound-mime-e2e.sh
#
# Env: ST_DEV_URL (default http://localhost:8080), E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD.
set -euo pipefail

BASE=${ST_DEV_URL:-http://localhost:8080}
ADMIN_EMAIL=${E2E_ADMIN_EMAIL:-admin@dev.test}
ADMIN_PASSWORD=${E2E_ADMIN_PASSWORD:-dev-admin-password-123}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
JAR=$WORK/cookies
RUN=$(date +%s)-$$
CUSTOMER=mime-$RUN@example.com

fail() { echo "FAIL: $*" >&2; exit 1; }
json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

me=$(curl -fsS -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"audience\":\"staff\",\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" "$BASE/api/v1/auth/login")
CSRF=$(printf '%s' "$me" | json 'd["csrf_token"]')
api() { # method path [json-body]
  local args=(-fsS -b "$JAR" -X "$1" -H "X-CSRF-Token: $CSRF" -H "Origin: $BASE")
  [ $# -ge 3 ] && args+=(-H 'Content-Type: application/json' -d "$3")
  curl "${args[@]}" "$BASE/api/v1$2"
}

# A known customer, so the message is not quarantined as unknown_sender (NOT-028).
api POST /agent/customers "{\"email\":\"$CUSTOMER\",\"name\":\"MIME E2E $RUN\"}" >/dev/null
mailbox=$(api POST /admin/inbound/mailboxes "{\"name\":\"MIME e2e $RUN\",\"kind\":\"webhook\",\"address\":\"support-$RUN@dev.test\"}")
MAILBOX_ID=$(printf '%s' "$mailbox" | json 'd["id"]')
URL=$(printf '%s' "$mailbox" | json 'd["webhook_url"]')
HOOK=$BASE/api/v1/inbound/mime/${URL##*/} # same token, reached through BASE
echo "mailbox $MAILBOX_ID → ${HOOK%/*}/<token>"

# Raw message: multipart with a text body and a small binary attachment, CRLF line ends.
python3 - "$WORK/msg.eml" "$CUSTOMER" "support-$RUN@dev.test" "$RUN" <<'EOF'
import sys, base64, os
path, frm, to, run = sys.argv[1:5]
att = base64.encodebytes(os.urandom(3000)).decode()
lines = [
  f"From: MIME E2E <{frm}>", f"To: {to}", f"Subject: Printer on fire ({run})",
  f"Message-ID: <mime-e2e-{run}@example.com>", "Date: Tue, 21 Oct 2026 08:00:00 +0000",
  "MIME-Version: 1.0", 'Content-Type: multipart/mixed; boundary="b1"', "",
  "--b1", "Content-Type: text/plain; charset=utf-8", "",
  "The printer on floor 2 is on fire again.", "", "Thanks, MIME E2E", "",
  "--b1", 'Content-Type: application/octet-stream; name="log.bin"',
  'Content-Disposition: attachment; filename="log.bin"', "Content-Transfer-Encoding: base64", "",
  *att.splitlines(), "--b1--", "",
]
open(path, "wb").write("\r\n".join(lines).encode())
EOF
SENT_SHA=$(shasum -a 256 "$WORK/msg.eml" | cut -d' ' -f1)
SIZE=$(wc -c <"$WORK/msg.eml" | tr -d ' ')

code=$(curl -sS -o "$WORK/resp" -w '%{http_code}' -X POST -H 'Content-Type: message/rfc822' \
  --data-binary @"$WORK/msg.eml" "$HOOK")
[ "$code" = 202 ] || fail "POST raw MIME: HTTP $code $(cat "$WORK/resp")"
echo "POST raw MIME ($SIZE bytes): 202"

# Wrong token: 404, no mailbox enumeration.
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: message/rfc822' \
  --data-binary @"$WORK/msg.eml" "${HOOK%/*}/not-a-real-token")
[ "$code" = 404 ] || fail "wrong token: HTTP $code (want 404)"
echo "wrong token: 404"

TICKET=""
for _ in $(seq 1 100); do
  row=$(api GET "/admin/inbound/messages?mailbox_id=$MAILBOX_ID&limit=5" |
    json '" ".join(str(m.get(k) or "-") for m in d["items"][:1] for k in ("status","reason","ticket_id"))')
  read -r st reason tid <<<"$row"
  if [ "${st:-}" = processed ] && [ "${tid:--}" != - ]; then TICKET=$tid; break; fi
  [ "${st:-}" = quarantined ] && fail "quarantined: ${reason:-}"
  sleep 0.1
done
[ -n "$TICKET" ] || fail "no ticket after 10 s (last inbound row: ${row:-none})"
echo "inbound message processed → ticket $TICKET"

detail=$(api GET "/agent/tickets/$TICKET")
printf '%s' "$detail" | python3 -c '
import sys, json
d = json.load(sys.stdin)
t = d.get("ticket", d)
assert "Printer on fire" in t["subject"], t["subject"]
' || fail "ticket subject mismatch"
ATT=$(printf '%s' "$detail" | python3 -c '
import sys, json
d = json.load(sys.stdin)
def walk(o):
    if isinstance(o, dict):
        if o.get("filename") == "original.eml" and "id" in o: yield o["id"]
        for v in o.values(): yield from walk(v)
    elif isinstance(o, list):
        for v in o: yield from walk(v)
print(next(walk(d), ""))')
[ -n "$ATT" ] || fail "no original.eml attachment on ticket $TICKET"
api GET "/agent/attachments/$ATT" >"$WORK/original.eml"
GOT_SHA=$(shasum -a 256 "$WORK/original.eml" | cut -d' ' -f1)
echo "posted   sha256 $SENT_SHA"
echo "original sha256 $GOT_SHA ($(wc -c <"$WORK/original.eml" | tr -d ' ') bytes)"
[ "$SENT_SHA" = "$GOT_SHA" ] || fail "original.eml differs from the posted bytes"
echo "PASS: raw MIME → 202 → ticket $TICKET; original.eml sha256 equals the posted bytes"
