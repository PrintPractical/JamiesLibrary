#!/usr/bin/env bash
# Builds the release binaries: the web app once, then the program for each platform,
# with SHA-256 sums. Usage: scripts/release.sh VERSION [OUTPUT_DIR]   (default dist/)
# Needs Go and Node with npm, and the web dependencies (npm ci in web/) installed.
# Output: OUTPUT_DIR/jamieslibrary-VERSION-OS-ARCH and OUTPUT_DIR/SHA256SUMS.
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:-}"
if [ -z "$version" ]; then
  echo "usage: scripts/release.sh VERSION [OUTPUT_DIR]" >&2
  exit 2
fi
case "$version" in
  *[!A-Za-z0-9._+-]*) echo "release.sh: VERSION may hold only letters, digits and . _ + -" >&2; exit 2 ;;
esac
out="${2:-dist}"

(cd web && npm run build)

mkdir -p "$out"
rm -f "$out"/jamieslibrary-* "$out"/SHA256SUMS

# No Windows build: the data directory lock uses syscall.Flock, which Windows lacks.
for target in linux/amd64 linux/arm64 darwin/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  name="jamieslibrary-$version-$os-$arch"
  echo "building $name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out/$name" ./cmd/jamieslibrary
done

(cd "$out" && sha256sum jamieslibrary-* > SHA256SUMS)
echo "done: $out/SHA256SUMS"
