#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
#
# Docs drift checks (DEP-028, DEP-032). Fails when the manual stops covering a
# configuration key or an API setting:
#   1. every FD_SMTP_* key in .env.example is in the §6.1 UI ↔ API ↔ env table;
#   2. every BrandingSettings property in api/openapi.yaml (incl. nested ones)
#      is named in §7.8 Branding;
#   3. every FD_DATABASE_URL, FD_BLOB_URL, FD_S3_* and FD_IMAP_* key in
#      .env.example is documented in docs/DOCUMENTATION.md.
#
#   scripts/docs-check.sh            # from any directory
# ROOT=… points it at another tree (used to prove it fails).
set -euo pipefail
ROOT=${ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
DOC=$ROOT/docs/DOCUMENTATION.md
ENV=$ROOT/.env.example
SPEC=$ROOT/api/openapi.yaml
fail=0
miss() { echo "::error::$1" >&2; fail=1; }

section() { # heading-prefix → text of that section up to the next heading of the same or higher level
  awk -v h="$1" 'index($0, h) == 1 {on = 1; print; next}
    on && /^#{2,3} [0-9]/ {exit} on' "$DOC"
}
env_keys() { grep -oE "^#? ?$1" "$ENV" | sed 's/^# *//' | sort -u; }

# 1. SMTP table.
smtp=$(section '### 6.1 ')
[ -n "$smtp" ] || miss "§6.1 not found in DOCUMENTATION.md"
n=0
for k in $(env_keys 'FD_SMTP_[A-Z_]+'); do
  n=$((n + 1))
  printf '%s\n' "$smtp" | grep -q "\`$k\`" || miss "$k (.env.example) is not in the §6.1 SMTP mapping table"
done
echo "FD_SMTP_* keys checked against §6.1: $n"

# 2. Branding settings.
branding=$(section '### 7.8 ')
[ -n "$branding" ] || miss "§7.8 not found in DOCUMENTATION.md"
props=$(awk '/^    BrandingSettings:/ {on = 1; next}
  on && /^    [A-Za-z]/ {exit}
  on && /^        [a-z_]+:$/ {sub(/:$/, ""); gsub(/ /, ""); print}
  on && /^            [a-z_]+:$/ && prev ~ /properties:/ {sub(/:$/, ""); gsub(/ /, ""); print}
  on && /^            [a-z_]+:$/ && inner {sub(/:$/, ""); gsub(/ /, ""); print}
  {if ($0 ~ /^          properties:$/) inner = 1; else if ($0 ~ /^        [a-z_]+:$/) inner = 0; prev = $0}' "$SPEC" |
  grep -vxE 'type|properties|items|oneOf|enum|format|pattern' | sort -u)
[ -n "$props" ] || miss "BrandingSettings not found in api/openapi.yaml"
n=0
for p in $props; do
  n=$((n + 1))
  printf '%s\n' "$branding" | grep -q "$p" || miss "BrandingSettings.$p (api/openapi.yaml) is not named in §7.8 Branding"
done
echo "BrandingSettings properties checked against §7.8: $n"

# 3. Postgres, S3 and IMAP keys.
n=0
for k in $(env_keys 'FD_(DATABASE_URL|BLOB_URL|S3_[A-Z_]+|IMAP_[A-Z_]+)'); do
  n=$((n + 1))
  grep -q "\`$k\`\|$k=" "$DOC" || miss "$k (.env.example) is not documented in docs/DOCUMENTATION.md"
done
echo "FD_DATABASE_URL/FD_BLOB_URL/FD_S3_*/FD_IMAP_* keys checked: $n"

[ "$fail" = 0 ] && echo "ok: docs cover every checked key"
exit "$fail"
