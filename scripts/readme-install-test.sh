#!/usr/bin/env bash
# Install check: runs the README's "Install with Docker" commands verbatim (the clone line is
# replaced by a copy of this tree) and its locked-out recovery command, then signs in.
# Usage: scripts/readme-install-test.sh   Needs Docker with Compose, ports 8080
# and 8443 free (the README uses host networking and the project name "jamieslibrary").
# Set PROJECT to run under another Compose project name than the README's (for a machine that
# already runs a "jamieslibrary" project); INSTALL_URL (default https://127.0.0.1:8443) if the app is reached some other way.
set -euo pipefail

src="$(cd "$(dirname "$0")/.." && pwd)"
project="${PROJECT:-jamieslibrary}"
url="${INSTALL_URL:-https://127.0.0.1:8443}"
work="$(mktemp -d)"
cleanup() {
  (cd "$work/jamieslibrary" 2>/dev/null && docker compose -p "$project" -f deploy/compose.lan.yml down -v >/dev/null 2>&1) || true
  rm -rf "$work"
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# The commands are the first sh block under "Install with Docker", and the recovery command
# is the one in the paragraph after it; both are taken from the README, not retyped here.
mkdir "$work/jamieslibrary"
(cd "$src" && git ls-files -z --cached --others --exclude-standard | xargs -0 cp --parents -t "$work/jamieslibrary")
cmds="$(awk '/^## Install with Docker/{s=1} s&&/^```sh/{b=1;next} b&&/^```/{exit} b' "$src/README.md" | grep -v '^git clone')"
recover="$(grep -o 'docker compose -p jamieslibrary -f deploy/compose.lan.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME' "$src/README.md" | head -1 || true)"
[ -n "$cmds" ] || fail "no install commands found in the README"
[ -n "$recover" ] || fail "no recovery command found in the README"
cmds="${cmds//-p jamieslibrary/-p $project}"; recover="${recover//-p jamieslibrary/-p $project}"
echo "--- README commands:"; echo "$cmds"

cd "$work/jamieslibrary"
out="$(bash -euo pipefail -c "$cmds" 2>&1)" || { echo "$out" >&2; fail "a README command failed"; }
code="$(echo "$out" | sed -n 's/.*SETUP CODE: *\([A-Z0-9-]*\).*/\1/p' | head -1)"
[ -n "$code" ] || fail "the README's last command printed no setup code: $out"

# curl runs in a container on the host network, so it reaches the app wherever Docker runs
# (the Docker host may not be this machine's own network).
c() { docker run --rm --network host curlimages/curl -sk "$@"; }
json='Content-Type: application/json'
for _ in $(seq 1 60); do c -f "$url/api/health" >/dev/null 2>&1 && break; sleep 1; done
c -f "$url/api/health" | grep -q '"ok"' || fail "health is not ok"

resp="$(c -i -H "$json" -d "{\"code\":\"$code\",\"username\":\"jamie\",\"display_name\":\"Jamie\",\"password\":\"first-password-123\"}" "$url/api/setup/admin")"
echo "$resp" | head -1 | grep -qE ' (200|201) ' || fail "creating the first administrator: $resp"
cookie="$(echo "$resp" | tr -d '\r' | sed -n 's/^[Ss]et-[Cc]ookie: *\([^;]*\).*/\1/p' | head -1)"
c -H "Cookie: $cookie" "$url/api/me" | grep -q 'administrator' || fail "the first user is not an administrator"
echo "ok: README install gives a running app and a first administrator"

# Recovery: the administrator "forgot" the password; the documented command is run as written
# (USERNAME replaced) and the temporary password it prints must sign them in.
rec="$(bash -c "${recover/USERNAME/jamie}" 2>&1)" || { echo "$rec" >&2; fail "the recovery command failed"; }
tmp="$(echo "$rec" | grep -oE '[A-Za-z0-9]{16}' | head -1)"
[ -n "$tmp" ] || fail "recovery printed no 16-character temporary password: $rec"
signin() { c -o /dev/null -w '%{http_code}' -H "$json" -d "{\"username\":\"jamie\",\"password\":\"$1\"}" "$url/api/auth/signin"; }
[ "$(signin first-password-123)" = 401 ] || fail "the old password still works"
[ "$(signin "$tmp")" = 200 ] || fail "the temporary password did not sign in"
echo "ok: documented recovery signs the administrator in again"
