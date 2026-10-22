#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Footprint measurement (DEP-012, CONVENTIONS §10). Runs the PRODUCTION compose
# (read_only, cap_drop ALL, mem_limit 256m, cpus 1) with a seeded database and
# reports, against target / hard limit:
#
#   image size     docker save | gzip -c | wc -c          ≤ 20 MB / ≤ 30 MB
#   idle RSS       docker stats after IDLE_SECONDS idle    ≤ 35 MB / ≤ 50 MB
#   idle CPU       mean of docker stats samples while idle ≈ 0%  / < 1%
#   cold start     docker start → first /readyz 200, median of RUNS ≤ 1 s / ≤ 3 s
#
# With --engine postgres the compose.postgres.yml overlay is added and the
# Postgres container's idle RSS is reported separately (target ≤ 60 MB).
#
# Usage: scripts/footprint.sh [--image featherdesk:local] [--engine sqlite|postgres]
#                             [--tickets 1000] [--idle 60] [--runs 5] [--no-gate]
#                             [--mem 256m] [--cpus 1]
# --mem/--cpus override the app's mem_limit/cpus (DEP-025 low-end host runs);
# the defaults are the values in deploy/compose.yml.
# Exit status: 0 = all hard limits met, 1 = a hard limit was exceeded, 2 = setup error.
# Appends a markdown table to $GITHUB_STEP_SUMMARY when set.
set -euo pipefail

IMAGE=${IMAGE:-featherdesk:local}
ENGINE=sqlite
TICKETS=1000
IDLE=60
RUNS=5
GATE=1
MEM=
CPUS=
PORT=${FD_FOOTPRINT_PORT:-18089}
while [ $# -gt 0 ]; do
  case $1 in
    --image) IMAGE=$2; shift 2 ;;
    --engine) ENGINE=$2; shift 2 ;;
    --tickets) TICKETS=$2; shift 2 ;;
    --idle) IDLE=$2; shift 2 ;;
    --runs) RUNS=$2; shift 2 ;;
    --no-gate) GATE=0; shift ;;
    --mem) MEM=$2; shift 2 ;;
    --cpus) CPUS=$2; shift 2 ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
PROJECT=fd-footprint-$$
FILES=(-f "$ROOT/deploy/compose.yml")
[ "$ENGINE" = postgres ] && FILES+=(-f "$ROOT/deploy/compose.postgres.yml")
if [ -n "$MEM$CPUS" ]; then
  { echo "services:"; echo "  app:"
    [ -n "$MEM" ] && echo "    mem_limit: $MEM"
    [ -n "$CPUS" ] && echo "    cpus: $CPUS"; } >"$WORK/limits.yml"
  FILES+=(-f "$WORK/limits.yml")
fi

# image → FD_IMAGE / FD_VERSION used by compose.yml
case $IMAGE in
  *@*) echo "pass a tag, not a digest" >&2; exit 2 ;;
  *:*) export FD_IMAGE=${IMAGE%:*} FD_VERSION=${IMAGE##*:} ;;
  *) export FD_IMAGE=$IMAGE FD_VERSION=latest ;;
esac
cat >"$WORK/app.env" <<EOF
FD_SECRET_KEY=$(head -c 32 /dev/urandom | base64)
FD_PUBLIC_URL=http://127.0.0.1:$PORT
FD_LOG_LEVEL=info
EOF
export FD_ENV_FILE="$WORK/app.env" FD_PORT=$PORT FD_BIND=127.0.0.1 FD_PG_PASSWORD=footprint-$$
dc() { docker compose -p "$PROJECT" "${FILES[@]}" "$@"; }
cleanup() { dc down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }
mib() { # "12.3MiB / 256MiB" → 12.3 (MiB); handles KiB/GiB/MB/kB
  awk -v s="$1" 'BEGIN{split(s,a," "); v=a[1]; n=v+0; u=v; gsub(/[0-9.]/,"",u);
    if(u=="KiB"||u=="kB")n/=1024; else if(u=="GiB"||u=="GB")n*=1024; else if(u=="B")n/=1048576; printf "%.1f", n}'
}
cid() { dc ps -q "$1"; }
wait_ready() { # prints seconds until /readyz 200, polled every 50 ms
  local t0=$1 i=0
  while :; do
    if curl -fsS -o /dev/null --max-time 1 "http://127.0.0.1:$PORT/readyz" 2>/dev/null; then
      awk -v a="$(now)" -v b="$t0" 'BEGIN{printf "%.3f", a-b}'; return 0
    fi
    i=$((i+1)); [ $i -gt 600 ] && { echo "timeout"; return 1; }
    sleep 0.05
  done
}

echo "== image $IMAGE ($ENGINE)"
IMG_BYTES=$(docker save "$IMAGE" | gzip -c | wc -c | tr -d ' ')
IMG_MB=$(awk -v b="$IMG_BYTES" 'BEGIN{printf "%.2f", b/1048576}')
echo "image compressed: $IMG_BYTES bytes ($IMG_MB MiB)"

echo "== seeding $TICKETS tickets"
[ "$ENGINE" = postgres ] && dc up -d --wait postgres >/dev/null
dc run --rm --no-deps app seed --tickets "$TICKETS" >"$WORK/seed.log" 2>&1 || { cat "$WORK/seed.log"; exit 2; }
grep -m1 seeded "$WORK/seed.log"

echo "== first boot"
dc up -d --wait app >/dev/null
APP=$(cid app)
MEMLINE=$(dc logs app 2>&1 | grep -m1 'memory limit derived' || true)
echo "memlimit log: ${MEMLINE:-<none>}"
LIMITS="mem_limit $(( $(docker inspect -f '{{.HostConfig.Memory}}' "$APP") / 1048576 ))MiB, cpus $(awk -v n="$(docker inspect -f '{{.HostConfig.NanoCpus}}' "$APP")" 'BEGIN{print n/1e9}')"
echo "app limits: $LIMITS"
SERVING=$(dc logs app 2>&1 | grep -m1 '"msg":"serving"' | grep -oE '"engine":"[a-z]+"' || true)
echo "serving: ${SERVING:-<none>}"
if [ "$ENGINE" = postgres ] && [ "$SERVING" != '"engine":"postgres"' ]; then echo "app is not on Postgres" >&2; exit 2; fi

# A freshly seeded database starts with a backlog: the queue replays the seeded
# tickets' events (notifications, search index) right after boot. Idle means
# after that catch-up, so wait until three samples in a row are under 1%.
echo "== settling (startup backlog, max 180 s)"
settle_start=$(date +%s) calm=0
while [ "$calm" -lt 3 ] && [ $(( $(date +%s) - settle_start )) -lt 180 ]; do
  c=$(docker stats --no-stream --format '{{.CPUPerc}}' "$APP" | tr -d '%')
  if awk -v c="$c" 'BEGIN{exit !(c < 1)}'; then calm=$((calm + 1)); else calm=0; fi
  sleep 2
done
echo "settled after $(( $(date +%s) - settle_start )) s"

echo "== idle ${IDLE}s (CPU sampled every 5 s)"
CPU_SAMPLES=()
end=$(( $(date +%s) + IDLE ))
while [ "$(date +%s)" -lt "$end" ]; do
  c=$(docker stats --no-stream --format '{{.CPUPerc}}' "$APP" | tr -d '%')
  CPU_SAMPLES+=("$c")
  sleep 5
done
IDLE_CPU=$(printf '%s\n' "${CPU_SAMPLES[@]}" | awk '{s+=$1;n++} END{printf "%.2f", (n?s/n:0)}')
IDLE_RSS=$(mib "$(docker stats --no-stream --format '{{.MemUsage}}' "$APP")")
PROC_RSS=$( (docker top "$APP" -o pid,rss 2>/dev/null || true) | awk 'NR>1{s+=$2} END{printf "%.1f", s/1024}')
PG_RSS="-"
if [ "$ENGINE" = postgres ]; then
  PG_RSS=$(mib "$(docker stats --no-stream --format '{{.MemUsage}}' "$(cid postgres)")")
fi
echo "idle RSS (cgroup, docker stats): $IDLE_RSS MiB · process RSS: $PROC_RSS MiB · idle CPU mean: $IDLE_CPU% · postgres RSS: $PG_RSS"

echo "== cold start x$RUNS"
STARTS=()
for i in $(seq 1 "$RUNS"); do
  docker stop -t 20 "$APP" >/dev/null
  t0=$(now)
  docker start "$APP" >/dev/null
  s=$(wait_ready "$t0")
  echo "run $i: ${s}s"
  STARTS+=("$s")
done
COLD=$(printf '%s\n' "${STARTS[@]}" | sort -n | awk '{a[NR]=$1} END{print a[int((NR+1)/2)]}')
OOM=$(docker inspect -f '{{.State.OOMKilled}}' "$APP")
RESTARTS=$(docker inspect -f '{{.RestartCount}}' "$APP")

FAIL=0
row() { # name value unit target limit cmp(le|lt)
  local verdict
  verdict=$(awk -v v="$2" -v l="$5" -v c="$6" 'BEGIN{ok=(c=="lt")?(v<l):(v<=l); print ok?"ok":"FAIL"}')
  [ "$verdict" = FAIL ] && FAIL=1
  printf '| %s | %s %s | %s | %s %s | %s |\n' "$1" "$2" "$3" "$4" "$( [ "$6" = lt ] && echo '<' || echo '≤')" "$5" "$verdict"
}
TABLE=$(
  echo "| Metric ($ENGINE, $TICKETS tickets, $LIMITS) | Measured | Target | Hard limit | Result |"
  echo "|---|---|---|---|---|"
  row "Image size (compressed, $(docker image inspect -f '{{.Architecture}}' "$IMAGE"))" "$IMG_MB" MiB "≤ 20 MiB" 30 le
  row "Idle RSS after ${IDLE}s" "$IDLE_RSS" MiB "≤ 35 MiB" 50 le
  row "Idle CPU (mean)" "$IDLE_CPU" "%" "≈ 0%" 1 lt
  row "Cold start to /readyz (median of $RUNS)" "$COLD" s "≤ 1 s" 3 le
  [ "$ENGINE" = postgres ] && echo "| Postgres idle RSS | $PG_RSS MiB | ≤ 60 MiB | (reported) | - |"
  echo "| OOM killed / restarts | $OOM / $RESTARTS | false / 0 | false / 0 | $( [ "$OOM" = false ] && echo ok || echo FAIL) |"
)
[ "$OOM" = false ] || FAIL=1
echo
echo "$TABLE"
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && { echo "### Footprint"; echo; echo "$TABLE"; } >>"$GITHUB_STEP_SUMMARY"
if [ "$GATE" = 1 ] && [ "$FAIL" = 1 ]; then
  echo "footprint: a hard limit was exceeded" >&2
  exit 1
fi
