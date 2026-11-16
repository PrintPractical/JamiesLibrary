#!/usr/bin/env bash
set -euo pipefail

# Install check: runs the README's "Install with Docker" commands (the clone line is replaced by
# a copy of this tree) and its locked-out recovery command, then signs in.
# Usage: scripts/readme-install-test.sh    Needs Docker with Compose.
#
# DO NOT RUN THIS ON A MACHINE THAT SERVES THE LIBRARY. It uses host networking, so ports 8080
# and 8443 must be free, and it deletes what it created with `down -v`. To keep it from touching
# a real install it never uses the README's project name or image name: the project is
# jltest-PID (PROJECT may set another, but "jamieslibrary" is refused), the copied compose file
# builds the image as jltest-PROJECT:latest, and it refuses to start if that project already has
# containers or a volume, or if 8080 or 8443 answers. The only difference from the README is
# those names.
# INSTALL_URL (default https://127.0.0.1:8443) is where the app is reached.

src="$(cd "$(dirname "$0")/.." && pwd)"
project="${PROJECT:-jltest-$$}"
url="${INSTALL_URL:-https://127.0.0.1:8443}"
work="$(mktemp -d)"
started=0
cleanup() {
  # Only what this run's own `up` started is removed.
  if [ "$started" = 1 ]; then
    (cd "$work/jamieslibrary" && docker compose -p "$project" -f deploy/compose.lan.yml down -v >/dev/null 2>&1) || true
    docker rmi "jltest-$project:latest" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

case "$project" in
  jamieslibrary*) fail "the project name $project is the README's: refusing, it could be a real install" ;;
esac
if [ -n "$(docker ps -a -q --filter "label=com.docker.compose.project=$project")" ]; then
  fail "project $project already has containers"
fi
if docker volume inspect "${project}_jamieslibrary-data" >/dev/null 2>&1; then
  fail "volume ${project}_jamieslibrary-data already exists"
fi
# curl runs in a container on the host network, so it reaches the app wherever Docker runs and
# also shows whether something already listens. Pinned by digest (curlimages/curl:8.11.1).
curlimage='curlimages/curl@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69'
c() { docker run --rm --network host "$curlimage" -sk "$@"; }
for port in 8080 8443; do
  rc=0; c --connect-timeout 2 -o /dev/null "http://127.0.0.1:$port/" >/dev/null 2>&1 || rc=$?
  [ "$rc" = 7 ] || fail "port $port is not free (curl exit $rc): something already listens there"
done

# The commands are the first sh block under "Install with Docker", and the recovery command
# is the one in the paragraph after it; both are taken from the README, not retyped here.
mkdir "$work/jamieslibrary"
(cd "$src" && git ls-files -z --cached --others --exclude-standard | xargs -0 cp --parents -t "$work/jamieslibrary")
cmds="$(awk '/^## Install with Docker/{s=1} s&&/^```sh/{b=1;next} b&&/^```/{exit} b' "$src/README.md" | grep -v '^git clone')"
recover="$(grep -o 'docker compose -p jamieslibrary -f deploy/compose.lan.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME' "$src/README.md" | head -1 || true)"
[ -n "$cmds" ] || fail "no install commands found in the README"
[ -n "$recover" ] || fail "no recovery command found in the README"
sed -i "s#image: jamieslibrary:latest#image: jltest-$project:latest#" "$work/jamieslibrary/deploy/compose.lan.yml"
grep -q "jltest-$project:latest" "$work/jamieslibrary/deploy/compose.lan.yml" || fail "the image name in compose.lan.yml was not replaced"
cmds="${cmds//-p jamieslibrary/-p $project}"; recover="${recover//-p jamieslibrary/-p $project}"
echo "--- README commands:"; echo "$cmds"

cd "$work/jamieslibrary"
started=1
out="$(bash -euo pipefail -c "$cmds" 2>&1)" || { echo "$out" >&2; fail "a README command failed"; }
code="$(echo "$out" | sed -n 's/.*SETUP CODE: *\([A-Z0-9-]*\).*/\1/p' | head -1)"
[ -n "$code" ] || fail "the README's last command printed no setup code: $out"

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
tmp="$(echo "$rec" | sed -n 's/^Temporary password for [^:]*: \([A-Za-z0-9]\{16\}\)$/\1/p' | head -1)"
[ -n "$tmp" ] || fail "recovery printed no 16-character temporary password: $rec"
signin() { c -o /dev/null -w '%{http_code}' -H "$json" -d "{\"username\":\"jamie\",\"password\":\"$1\"}" "$url/api/auth/signin"; }
[ "$(signin first-password-123)" = 401 ] || fail "the old password still works"
[ "$(signin "$tmp")" = 200 ] || fail "the temporary password did not sign in"
echo "ok: documented recovery signs the administrator in again"
