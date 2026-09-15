#!/usr/bin/env bash
# rotate-admin-token.sh — rotate the freebuff-proxy admin dashboard token.
#
# Uses the real POST /admin/api/change-password flow (login -> fb_csrf ->
# X-CSRF-Token -> JSON), so the server's own dual-layer persistence runs:
# .env (0600) + settings overlay (source: db). No container restart needed.
#
# Token material never appears in argv or terminal output: login and body
# values are passed to curl from 0600 tmpfs files, and nothing is echoed.
#
# Usage:
#   scripts/rotate-admin-token.sh                    # rotate to random 20-char token
#   TOKEN_FILE=/path/to/token scripts/rotate-admin-token.sh   # supply your own
#     (TOKEN_FILE content must be JSON/URL-safe: [A-Za-z0-9._~-], >=16 chars)
#
# Env overrides: BASE_URL (default http://127.0.0.1:3457),
# ENV_FILE (default <repo>/.env).
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:3457}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"
TOKEN_FILE="${TOKEN_FILE:-}"

read_env_token() {
    # .env is 0600 and may be owned by another user; fall back to sudo.
    local t=""
    t="$(grep '^ADMIN_TOKEN=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- || true)"
    if [ -z "$t" ]; then
        t="$(sudo -n grep '^ADMIN_TOKEN=' "$ENV_FILE" 2>/dev/null | tail -1 | cut -d= -f2- || true)"
    fi
    printf '%s' "$t"
}

CUR="$(read_env_token)"
[ -n "$CUR" ] || {
    echo "FAIL: could not read current ADMIN_TOKEN from $ENV_FILE" >&2
    exit 1
}

TMP="$(mktemp -d /dev/shm/fb-rotate.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
chmod 700 "$TMP"

if [ -n "$TOKEN_FILE" ]; then
    NEW="$(tr -d '\n\r' < "$TOKEN_FILE")"
else
    # 24 bytes of urandom (192 bits) -> base64 -> strip -> 20 chars.
    NEW="$(head -c 24 /dev/urandom | base64 | tr -d '=+/' | cut -c1-20)"
fi
[ "${#NEW}" -ge 16 ] || { echo "FAIL: replacement token must be >=16 chars" >&2; exit 1; }

# 1) Login with the current token (value read from file, not argv).
printf '%s' "$CUR" > "$TMP/cur"
code="$(curl -s -o /dev/null -w '%{http_code}' -c "$TMP/jar" \
    --data-urlencode "token@$TMP/cur" "$BASE_URL/admin/login")"
[ "$code" = "302" ] || { echo "FAIL: login with current token returned $code" >&2; exit 1; }

CSRF="$(awk '$6=="fb_csrf"{print $NF}' "$TMP/jar")"
[ -n "$CSRF" ] || { echo "FAIL: no fb_csrf cookie after login" >&2; exit 1; }

# 2) Rotate via the JSON API (body from file, not argv).
printf '{"current_password":"%s","new_password":"%s"}' "$CUR" "$NEW" > "$TMP/body"
resp="$(curl -s -b "$TMP/jar" -H "X-CSRF-Token: $CSRF" \
    -H 'Content-Type: application/json' -d @"$TMP/body" \
    "$BASE_URL/admin/api/change-password")"
printf '%s' "$resp" | grep -q '"ok":true' || {
    echo "FAIL: rotation rejected: $(printf '%s' "$resp" | head -c 120)" >&2
    exit 1
}

# 3) Verify: new token accepted, old token rejected.
printf '%s' "$NEW" > "$TMP/new"
code="$(curl -s -o /dev/null -w '%{http_code}' --data-urlencode "token@$TMP/new" "$BASE_URL/admin/login")"
[ "$code" = "302" ] || { echo "FAIL: new-token login returned $code" >&2; exit 1; }
code="$(curl -s -o /dev/null -w '%{http_code}' --data-urlencode "token@$TMP/cur" "$BASE_URL/admin/login")"
[ "$code" != "302" ] || { echo "FAIL: old token still logs in" >&2; exit 1; }

# 4) Verify the server persisted the rotation to .env (give the atomic
#    write a beat; the overlay write happens in the same request).
sleep 1
PERSISTED="$(read_env_token)"
[ "$PERSISTED" = "$NEW" ] || { echo "FAIL: .env does not match rotated token" >&2; exit 1; }

echo "OK: ADMIN_TOKEN rotated (${#NEW} chars). Server persisted .env + overlay; no restart needed."
