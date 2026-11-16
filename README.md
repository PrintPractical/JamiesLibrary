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

## Install with Docker

Step-by-step guides, each with installation, configuration, network access, security and maintenance (backup and upgrade): [on a home or office network](docs/install-lan.md) and [on the public internet](docs/install-public.md). A short version follows.

You need Docker with the Compose plugin, on a 64-bit x86 or ARM (Raspberry Pi 4 or newer) Linux machine. Host networking, used below, is a Linux feature.

```sh
git clone <this repository> jamieslibrary && cd jamieslibrary
docker compose -p jamieslibrary -f deploy/compose.lan.yml up -d --build
docker compose -p jamieslibrary -f deploy/compose.lan.yml logs | grep "SETUP CODE"
```

The last command prints the one-time `SETUP CODE: XXXX-XXXX-XXXX-XXXX`. Open `https://THIS-MACHINE:8443` in a browser and enter the code to create the first administrator. Phones and other computers should first follow the trust guide at `http://THIS-MACHINE:8080/trust/`, which is also where the local certificate is downloaded; the guide says what a device trusts by doing the step.

Everything the library keeps is in one Docker volume, `jamieslibrary_jamieslibrary-data` (the project name `-p jamieslibrary` is part of it), mounted at `/data` in the container. To back it up, stop the container, copy the volume's contents, and start it again; the guides give the commands. The backup holds password hashes, every patron's history and the certificate key: keep it private. To recover a locked-out administrator: `docker compose -p jamieslibrary -f deploy/compose.lan.yml exec jamieslibrary /usr/local/bin/jamieslibrary admin reset-password USERNAME`.

The container runs as user 65532 with a read-only root filesystem; only `/data` is writable. If you replace the volume with a folder on the host, the folder must be owned by user 65532. The image is built for `linux/amd64` and `linux/arm64`: `docker buildx build -f deploy/Dockerfile --platform linux/amd64,linux/arm64 -t jamieslibrary .` (add `--build-arg VERSION=1.0.0` to stamp the version).

**On the internet, behind a reverse proxy:** `deploy/compose.public.yml` runs the app over plain HTTP on a private network, with Caddy in front getting the HTTPS certificate. Put your domain name in `deploy/Caddyfile`, open ports 80 and 443, and run `docker compose -p jamieslibrary -f deploy/compose.public.yml up -d --build`. It sets `JL_HTTP_ONLY=1`, `JL_BIND_ADDRESS` and `JL_TRUSTED_PROXIES` for you; `JL_TRUSTED_PROXIES` lists only the proxy's own fixed address (`172.30.0.2/32`), never the whole network, whose gateway is the host itself: if you use another proxy, list only that proxy's address (see the settings table and the [public guide](docs/install-public.md#behind-a-reverse-proxy)). The app offers the same functions in both setups.

## Release binaries

`scripts/release.sh VERSION [DIR]` builds the web app once, then the program for `linux/amd64`, `linux/arm64` and `darwin/arm64` into `DIR` (default `dist/`), with a `SHA256SUMS` file to check them with (`sha256sum -c SHA256SUMS`). Windows isn't built: the data directory lock needs a Unix system call.

A binary doesn't set a memory limit by itself. On a small machine (a Raspberry Pi 4 with a library of 20,000 copies should stay under 512 MB) start it with `GOMEMLIMIT=400MiB`, which makes Go's memory manager work harder before the program grows past that. The Docker image sets it already.

## Settings

Each setting is a flag or a `JL_*` environment variable. A flag wins over the variable.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--data` | `JL_DATA_DIR` | `./data` | Where all state is kept |
| `--port` | `JL_HTTP_PORT` | `8080` | HTTP port |
| `--bind` | `JL_BIND_ADDRESS` | empty (every interface) | IP address both listeners bind to. Use `127.0.0.1` when a reverse proxy runs on the same host, so only the proxy can reach the app |
| `--https-port` | `JL_HTTPS_PORT` | `8443` | HTTPS port |
| `--log-level` | `JL_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| | `JL_ADDRESSES` | empty | Extra local IP addresses the local HTTPS certificate must name, comma or space separated, for a server that can't see its own address (Docker without host networking). Only private-range addresses are used |
| | `JL_HOSTNAMES` | empty | Your own host names for the library, such as `library.myschool.example`, which the local certificate authority may vouch for. Read at the very first start only |
| | `JL_TLS_CERT`, `JL_TLS_KEY` | empty | Your own certificate chain and key (PEM), used instead of the local certificate authority. Both or neither; a renewed file is picked up without a restart |
| | `JL_HTTP_ONLY` | `0` | `1` serves plain HTTP only, for a reverse proxy that adds HTTPS. Can't be used with `JL_TLS_CERT` |
| | `JL_TRUSTED_PROXIES` | empty | Reverse proxies (CIDR ranges or bare addresses, comma or space separated) whose `X-Forwarded-For` is believed when counting failed sign-ins. List only the proxy's own address |

A bad value stops the server with a message naming it. SIGTERM or SIGINT shuts the server down, letting running requests finish for up to 10 seconds.

## Develop

`scripts/check.sh` runs everything: in `web/` the typecheck, lint, tests and build, then `gofmt`, `go vet` and `go test`.
Install dependencies first with `cd web && npm ci` and `go mod download`; the check itself needs no network.
`scripts/readme-install-test.sh` follows the Docker install steps above on a clean checkout, then the locked-out recovery steps. Don't run it on a machine that serves the library: it needs ports 8080 and 8443 free and refuses to start otherwise, and it uses its own project, image and volume names (never `jamieslibrary`) and removes only those; `scripts/loadtest.sh IMAGE` is the 20,000-copy load and memory check.
For the web app with live reload, run the server and `npm run dev` in `web/`; it proxies `/api` to port 8080.

## Documentation

- [Architecture](docs/architecture.md): the parts, the repository layout, and how to add a migration, a route or a theme.
- [Installing on a home or office network](docs/install-lan.md) and [on the public internet](docs/install-public.md): installation, configuration, network access, security, backup and upgrade.
- [Data and upgrades](docs/data-and-upgrades.md): what the data directory holds, backing it up, and what to do when an upgrade refuses to start.
- [Finding books](docs/guide-browsing.md): browse, search, book pages, ratings and reviews, and the public catalog.
- [Working the desk](docs/guide-desk.md): for librarians: checking out and in, renewing, overdue loans, looking up a patron and re-issuing a card.
- [Borrowing books](docs/guide-patron.md): for patrons: your books and due dates, renewing, history, your library card and self-checkout.
- [Looking after the catalog](docs/guide-catalog.md): for librarians: adding books by scanning, copies and how they are identified, locations, changing and removing, and printing labels.
- [Administrator's guide](docs/guide-admin.md): first-run setup, settings, statistics and the activity log.
- [Accounts, permissions, invitations and recovery](docs/accounts.md): signing in, registering with a code, the 16 permissions and three roles, managing people, and recovering a locked-out administrator.
- [Features](FEATURES.md) and [Changelog](CHANGELOG.md).
