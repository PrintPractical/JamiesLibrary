# Jamie's Library

A lending-library manager for a community library: catalogue books, issue
library cards, and check items out and in. It runs as one program, a single Go
binary with its web app built in, and keeps everything in one data directory.

Licensed under the [Apache License 2.0](LICENSE).

## Build and run from source

You need Go 1.27 or newer and Node.js 22.12 or newer (Vite 8 also accepts 20.19 and later) with npm.

```sh
cd web && npm ci && npm run build && cd ..   # builds the web app into internal/webui/dist
go build -ldflags "-X main.version=0.1.0" -o jamieslibrary ./cmd/jamieslibrary
./jamieslibrary            # same as: ./jamieslibrary serve
./jamieslibrary version
```

By default the server keeps its data in `./data`, created on first start (see [Data and upgrades](docs/data-and-upgrades.md)).
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
Install dependencies first with `cd web && npm ci` and `go mod download`; the check itself needs no network.
For the web app with live reload, run the server and `npm run dev` in `web/`; it proxies `/api` to port 8080.

## Documentation

- [Architecture](docs/architecture.md): the parts, the repository layout, and how to add a migration, a route or a theme.
- [Data and upgrades](docs/data-and-upgrades.md): what the data directory holds, backing it up, and what to do when an upgrade refuses to start.
- [Features](FEATURES.md) and [Changelog](CHANGELOG.md).
