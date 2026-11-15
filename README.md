# Jamie's Library

A lending-library manager for a community library: catalogue books, issue
library cards, and check items out and in. It runs as one program, a single Go
binary with its web app built in, and keeps everything in one data directory.

Licensed under the [Apache License 2.0](LICENSE).

## Build and run from source

You need Go 1.27 and Node.js with npm.

```sh
cd web && npm ci && npm run build && cd ..   # builds the web app into internal/webui/dist
go build -ldflags "-X main.version=0.1.0" -o jamieslibrary ./cmd/jamieslibrary
./jamieslibrary            # same as: ./jamieslibrary serve
./jamieslibrary version
```

Then open <http://localhost:8080>. `GET /api/health` answers `{"status":"ok",...}`.
Without the web build step the binary still builds and runs, but serves a page saying the web app is missing.

## Settings

Each setting is a flag or a `JL_*` environment variable. A flag wins over the variable.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data` | `JL_DATA_DIR` | `./data` | Where all state is kept |
| `--port` | `JL_HTTP_PORT` | `8080` | HTTP port |
| `--log-level` | `JL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

A bad value stops the server with a message naming it. SIGTERM or SIGINT shuts the server down, letting running requests finish for up to 10 seconds.

## Develop

`scripts/check.sh` runs everything: in `web/` the typecheck, lint, tests and build, then `gofmt`, `go vet` and `go test`.
Install dependencies first with `cd web && npm ci` and `go mod download`.
For the web app with live reload, run the server and `npm run dev` in `web/`; it proxies `/api` to port 8080.
