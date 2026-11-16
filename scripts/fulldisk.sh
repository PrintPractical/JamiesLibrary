#!/usr/bin/env bash
# Full-disk check: the library in a container whose /data is a 3 MB tmpfs, filled until
# writes are refused. Usage: scripts/fulldisk.sh IMAGE   (an image built from deploy/Dockerfile)
# Needs Docker and Go. FULLDISK_PORT (default 18079) is the local port the app is published on.
set -euo pipefail

cd "$(dirname "$0")/.."
image="${1:-}"
if [ -z "$image" ]; then
  echo "usage: scripts/fulldisk.sh IMAGE" >&2
  exit 2
fi
port="${FULLDISK_PORT:-18079}"
name="jl-fulldisk-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker volume create --driver local --opt type=tmpfs --opt device=tmpfs \
  --opt o=size=3m,uid=65532,gid=65532,mode=0700 "$name" >/dev/null
docker run -d --name "$name" --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  -v "$name":/data -e JL_HTTP_ONLY=1 -e JL_HTTP_PORT=8080 -p "127.0.0.1:$port:8080" "$image" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
code="$(docker logs "$name" 2>&1 | sed -n 's/.*SETUP CODE: *\([A-Z0-9-]*\).*/\1/p' | head -1)"
if [ -z "$code" ]; then
  echo "fulldisk: no setup code in the container's output:" >&2
  docker logs "$name" >&2
  exit 1
fi

status=0
JL_FULLDISK_URL="http://127.0.0.1:$port" JL_FULLDISK_SETUP_CODE="$code" JL_FULLDISK_VOLUME="$name" \
  go test -tags testhooks ./internal/e2e -run TestFullDisk -count=1 -v -timeout 10m || status=$?
if [ "$status" -ne 0 ]; then
  echo "--- the container's errors and warnings (last 20) ---" >&2
  docker logs "$name" 2>&1 | grep -E 'level=(ERROR|WARN)' | tail -20 >&2 || true
fi
exit "$status"
