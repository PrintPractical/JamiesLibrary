#!/usr/bin/env bash
# The repository's check: web app (typecheck, lint, tests, build), then Go (format, vet, tests).
# Needs the dependencies installed already (npm ci in web/, go mod download); uses no network.
# web/go.mod stops the Go tools descending into web/node_modules.
set -euo pipefail

cd "$(dirname "$0")/.."

(
  cd web
  npm run typecheck
  npm run lint
  npm test
  npm run build
)

unformatted="$(gofmt -l cmd internal)"
if [ -n "$unformatted" ]; then
  echo "gofmt: these files need formatting:" >&2
  echo "$unformatted" >&2
  exit 1
fi
go vet ./...
go vet -tags testhooks ./...
go test ./...
# The test hooks (clock, fake provider, migration failure) exist only in this build; the API tests need it.
go test -tags testhooks ./...
