#!/usr/bin/env bash
# Load and memory check: 20,000 copies, 500 patrons, 100 clients searching at once, in
# a container limited to 512 MB. Usage: scripts/loadtest.sh IMAGE   (an image built from deploy/Dockerfile)
# Needs Docker and Go. The numbers are the container's on this machine, not a Raspberry Pi's.
# LOAD_PORT (default 18077) is the local port the container's app is published on.
set -euo pipefail

cd "$(dirname "$0")/.."
image="${1:-}"
if [ -z "$image" ]; then
  echo "usage: scripts/loadtest.sh IMAGE" >&2
  exit 2
fi
port="${LOAD_PORT:-18077}"
name="jl-loadtest-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# The image's own defaults (GOMEMLIMIT, user, data volume); only the listener is plain HTTP so
# the test needs no certificate. Swap is off, so 512 MB is a hard limit.
docker run -d --name "$name" --memory 512m --memory-swap 512m --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true -v "$name":/data \
  -e JL_HTTP_ONLY=1 -e JL_HTTP_PORT=8080 -p "127.0.0.1:$port:8080" "$image" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
code="$(docker logs "$name" 2>&1 | sed -n 's/.*SETUP CODE: *\([A-Z0-9-]*\).*/\1/p' | head -1)"
if [ -z "$code" ]; then
  echo "loadtest: no setup code in the container's output:" >&2
  docker logs "$name" >&2
  exit 1
fi

status=0
JL_LOAD_URL="http://127.0.0.1:$port" JL_LOAD_SETUP_CODE="$code" JL_LOAD_CONTAINER="$name" \
  go test ./internal/e2e -run TestTwentyThousandCopies -count=1 -v -timeout 40m || status=$?
if [ "$status" -ne 0 ]; then
  echo "--- the container's errors and warnings (last 20) ---" >&2
  docker logs "$name" 2>&1 | grep -E 'level=(ERROR|WARN)' | tail -20 >&2 || true
fi
exit "$status"
