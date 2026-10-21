#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# DEP-015: bring up each reverse-proxy example from deploy/examples in front of
# the app image, then over HTTPS check that (1) the SPA is served, (2) the
# login cookie is Secure, (3) the app logs the client IP from X-Forwarded-For,
# never the proxy container IP, and (4) a WebSocket upgrade with subprotocol
# simpletickets.v1 answers ping with pong (tools/loadtest -probe).
#
# Only the parts that cannot work on a laptop are rewritten on the fly: the
# public hostname becomes localhost, certificates are self-signed / internal,
# and the upstream is the app container instead of 127.0.0.1.
#
# Usage: scripts/proxy-test.sh [--image simpletickets:local] [caddy] [nginx] [traefik]
set -euo pipefail
IMAGE=simpletickets:local
WANT=()
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    *) WANT+=("$1"); shift ;;
  esac
done
[ ${#WANT[@]} -gt 0 ] || WANT=(caddy nginx traefik)
ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
NET=st-proxy-$$
PORT=${ST_PROXY_PORT:-18443}
NGINX_IMAGE=nginx:1.29-alpine@sha256:5616878291a2eed594aee8db4dade5878cf7edcb475e59193904b198d9b830de
CADDY_IMAGE=caddy:2.10-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
TRAEFIK_IMAGE=traefik:v3.7.16@sha256:6d3e069ee484ab1da5adfdda555a75eb9b1545ef09096a0cc7c3043101986a20
BASE=https://localhost:$PORT
ADMIN=admin@proxy.test
PASS=proxy-admin-password-123
FAKE_CLIENT=203.0.113.77

cleanup() {
  docker rm -f st-proxy-app-$$ st-proxy-px-$$ >/dev/null 2>&1 || true
  docker volume rm -f st-proxy-data-$$ >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT
docker network create "$NET" >/dev/null
SUBNET=$(docker network inspect -f '{{(index .IPAM.Config 0).Subnet}}' "$NET")
(cd "$ROOT" && go build -o "$WORK/loadtest" ./tools/loadtest)
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
  -keyout "$WORK/key.pem" -out "$WORK/cert.pem" >/dev/null 2>&1

start_app() { # $1 = extra docker args (labels)
  docker rm -f st-proxy-app-$$ >/dev/null 2>&1 || true
  docker volume rm -f st-proxy-data-$$ >/dev/null 2>&1 || true
  docker run --rm -i -v st-proxy-data-$$:/data -e ST_SECRET_KEY=proxy-test-secret-key-0123456789abcdef \
    "$IMAGE" create-admin --email "$ADMIN" --password-stdin <<<"$PASS" >/dev/null
  # shellcheck disable=SC2086
  docker run -d --name st-proxy-app-$$ --network "$NET" --network-alias app $1 \
    --read-only --tmpfs /tmp --cap-drop ALL --security-opt no-new-privileges \
    -v st-proxy-data-$$:/data \
    -e ST_SECRET_KEY=proxy-test-secret-key-0123456789abcdef -e ST_PUBLIC_URL="$BASE" \
    -e ST_TRUSTED_PROXIES="$SUBNET" -e ST_LOG_LEVEL=info "$IMAGE" >/dev/null
  for _ in $(seq 1 100); do
    [ "$(docker inspect -f '{{.State.Health.Status}}' st-proxy-app-$$ 2>/dev/null)" = healthy ] && return 0
    sleep 0.3
  done
  docker logs st-proxy-app-$$; return 1
}

check() { # $1 = proxy name
  local name=$1 ok=0
  for _ in $(seq 1 60); do curl -ksf -o /dev/null "$BASE/readyz" && ok=1 && break; sleep 0.5; done
  [ $ok = 1 ] || { echo "[$name] proxy not answering"; docker logs st-proxy-px-$$ 2>&1 | tail -20; return 1; }
  curl -ksf "$BASE/" | grep -qi '<div id="app"' || { echo "[$name] SPA not served"; return 1; }
  local hdr
  hdr=$(curl -ks -D - -o /dev/null -H 'Content-Type: application/json' -H "Origin: $BASE" -H "X-Forwarded-For: $FAKE_CLIENT" \
    --data "{\"audience\":\"staff\",\"email\":\"$ADMIN\",\"password\":\"$PASS\"}" "$BASE/api/v1/auth/login")
  echo "$hdr" | grep -i '^set-cookie: st_session=' | grep -qi 'secure' || { echo "[$name] login cookie not Secure:"; echo "$hdr"; return 1; }
  sleep 0.3
  # The client is this host, reaching the proxy through the published port, so
  # the proxy sees the network gateway. nginx's example appends to an incoming
  # X-Forwarded-For (the app then walks past trusted hops to $FAKE_CLIENT);
  # Caddy and Traefik replace it with the address they saw. Either is right.
  # The proxy container's own IP is wrong.
  local px_ip gw logged
  px_ip=$(docker inspect -f "{{(index .NetworkSettings.Networks \"$NET\").IPAddress}}" st-proxy-px-$$)
  gw=$(docker network inspect -f '{{(index .IPAM.Config 0).Gateway}}' "$NET")
  logged=$(docker logs st-proxy-app-$$ 2>&1 | grep '/api/v1/auth/login' | tail -1 | sed -E 's/.*"ip":"([^"]+)".*/\1/')
  if [ "$logged" = "$px_ip" ] || { [ "$logged" != "$FAKE_CLIENT" ] && [ "$logged" != "$gw" ]; }; then
    echo "[$name] app logged ip=$logged (proxy=$px_ip, gateway=$gw, forwarded=$FAKE_CLIENT)"; return 1
  fi
  echo "[$name] client IP logged by the app: $logged (proxy container is $px_ip)"
  "$WORK/loadtest" -probe -k -url "$BASE" -admin-email "$ADMIN" -admin-password "$PASS"
  echo "[$name] PASS: SPA over TLS, Secure cookie, client IP via X-Forwarded-For, WebSocket ping/pong"
}

fails=0
for p in "${WANT[@]}"; do
  echo "== $p"
  docker rm -f st-proxy-px-$$ >/dev/null 2>&1 || true
  case $p in
    caddy)
      start_app ""
      sed -e "s/^help.example.com {/localhost:443 {\n\ttls internal/" -e 's/reverse_proxy 127.0.0.1:8080/reverse_proxy app:8080/' \
        "$ROOT/deploy/examples/Caddyfile" >"$WORK/Caddyfile"
      docker run -d --name st-proxy-px-$$ --network "$NET" -p 127.0.0.1:$PORT:443 \
        -v "$WORK/Caddyfile:/etc/caddy/Caddyfile:ro" "$CADDY_IMAGE" >/dev/null ;;
    nginx)
      start_app ""
      sed -e 's/server_name help.example.com;/server_name localhost;/' -e 's#proxy_pass http://127.0.0.1:8080;#proxy_pass http://app:8080;#' \
        -e 's#/etc/letsencrypt/live/help.example.com/fullchain.pem#/certs/cert.pem#' -e 's#/etc/letsencrypt/live/help.example.com/privkey.pem#/certs/key.pem#' \
        "$ROOT/deploy/examples/nginx.conf" >"$WORK/nginx.conf"
      docker run -d --name st-proxy-px-$$ --network "$NET" -p 127.0.0.1:$PORT:443 \
        -v "$WORK/nginx.conf:/etc/nginx/conf.d/default.conf:ro" -v "$WORK:/certs:ro" "$NGINX_IMAGE" >/dev/null ;;
    traefik)
      # Labels from deploy/examples/traefik.compose.yml; Host → localhost, the
      # ACME resolver → Traefik's default self-signed certificate.
      labels=$(awk '/labels:/{f=1;next} f&&/^[^ ]/{f=0} f&&/traefik\./{sub(/^ +/,""); print}' "$ROOT/deploy/examples/traefik.compose.yml" \
        | grep -v certresolver | sed -e 's/Host(`help.example.com`)/Host(`localhost`)/' -e "s/traefik.docker.network: proxy/traefik.docker.network: $NET/" \
        | awk -F': ' '{v=$2; gsub(/"/,"",v); printf "--label %s=%s ", $1, v}')
      labels="$labels --label traefik.http.routers.simpletickets.tls=true"
      start_app "$labels"
      docker run -d --name st-proxy-px-$$ --network "$NET" -p 127.0.0.1:$PORT:443 \
        -v /var/run/docker.sock:/var/run/docker.sock:ro "$TRAEFIK_IMAGE" \
        --providers.docker=true --providers.docker.exposedbydefault=false \
        --entrypoints.websecure.address=:443 --log.level=WARN >/dev/null ;;
    *) echo "unknown proxy $p"; fails=$((fails+1)); continue ;;
  esac
  check "$p" || fails=$((fails+1))
done
exit $fails
