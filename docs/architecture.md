# Architecture

Jamie's Library is one program: a Go server with the web app built into it. It keeps all its state in one data directory and talks to no other service.

```
browser (React app) --HTTP--> jamieslibrary binary --> SQLite database and files in the data directory
```

The server answers three kinds of request:

- `/api/...`: a JSON API, the only way the web app reads or changes anything. Today that is `GET /api/health`; any other `/api/` path answers a JSON `not_found` error.
- `/media/...`: reserved for covers and the logo. Nothing is served there yet.
- everything else: the web app, from files embedded in the binary. A path that names no file and has no extension gets `index.html` (the app's own router handles it); a missing file with an extension is a 404, so a script is never answered with a page.

## Repository layout

```
cmd/jamieslibrary/        main: the commands `serve` (the default) and `version`
internal/config/          flags and JL_* environment variables -> Config
internal/store/           the data directory: lock, SQLite, migrations, pre-upgrade backup, tmp/
internal/apperr/          the typed error services return, and its HTTP status
internal/httpapi/         the router, request logging, JSON and error helpers, the server and its timeouts
internal/webui/           go:embed of the built web app (dist/)
internal/barcode/         SVG codes and PDF label sheets (see below)
web/                      the web app: React, TypeScript, Vite
scripts/check.sh          the repository's check
docs/                     this documentation
```

`internal/webui/dist/` holds only a `.gitkeep` until you run `npm run build` in `web/`, which writes the app there. Without it the binary still builds, and serves a page saying the web app is missing.

Further packages for each area of the library are added beside these as they are built. Each owns its own tables and rules: it exposes a service whose methods take a `context.Context` and plain inputs and return values or an `*apperr.Error`, it never sees HTTP, and other packages go through its service, not its tables. Handlers in `internal/httpapi` only decode, call one service method and encode.

## Starting up

`serve` does this in order, and stops with a message on the first failure:

1. Reads the settings (flags beat `JL_*` variables, which beat defaults; see the README).
2. Makes the data directory ready: creates it (mode 0700) if missing, and proves it is writable.
3. Takes an exclusive lock on `lock` in it. A second instance on the same directory is refused.
4. Opens `library.db` with write-ahead logging, foreign keys on and a 5 second busy timeout, and brings the schema up to date (see [Data and upgrades](data-and-upgrades.md)).
5. Starts removing old files from `tmp/` (at start, then hourly) and listens on the port.

SIGTERM or SIGINT stops accepting connections, lets running requests finish for up to 10 seconds, closes the database and exits 0.

Server limits: headers up to 64 KiB, read-header timeout 10 s, read and write timeouts 60 s, idle timeout 120 s.

## Errors

API errors have one shape:

```json
{"error": {"code": "not_found", "message": "...", "field": "..."}}
```

`message` is a sentence a person can act on; `field` appears when one input is at fault. Codes and statuses: `bad_request` 400, `unauthorized` 401, `forbidden` 403, `not_found` 404, `conflict` 409, `too_large` 413, `invalid` 422, `throttled` 429, `internal` 500, `unavailable` 503. An unexpected failure is logged with a request ID and answered as a 500 carrying only that ID, never the internal text. Every response has an `X-Request-ID` header, and each request is logged as one line (method, path, status, duration; never the query or body).

## The database and its migrations

The database is SQLite (`modernc.org/sqlite`, pure Go, so no C compiler is needed and ARM builds cross-compile). Every transaction starts as `BEGIN IMMEDIATE`, so writers queue up rather than fail halfway. `store.WithTx(ctx, func(tx) error)` runs a function in one such transaction and commits or rolls back.

**To add a migration**, put a file named `NNNN_what.sql` (for example `0002_loans.sql`) in `internal/store/migrations/`:

- Numbers start at 1, are unique and only grow. Never edit a file once it has been released: add a new one.
- A file is plain SQL run as one batch. All pending files run in a single transaction, so an upgrade applies completely or not at all.
- A migration may only change the database. One that must change files in the data directory has to copy them into the backup first.
- A file that is misnamed, or repeats a number, stops the program from starting.
- Applied versions are recorded in the `schema_migrations` table (`version`, `name`, `applied_at`).

**Writes that must agree go in one transaction.** When one action changes several areas (a checkout and its activity record, say), the service methods involved take a `store.Tx` (an alias for `*sql.Tx`) and the caller opens the transaction with `store.WithTx`, so either every change is kept or none is. A service never opens a second transaction inside the caller's. Reporting is the one package allowed to read other areas' tables directly, with read-only SQL; every other package goes through the owning service.

Tests open a real database in a temporary directory; `store.Options{Migrations: fsys}` replaces the embedded migrations for a test.

## Barcodes

`internal/barcode` is pure rendering and knows nothing of the database. It makes SVG codes (Code 128 of up to 32 printable ASCII characters, EAN-13 from an ISBN-13, QR) and PDF label sheets (Avery 5160, Avery L7160, and a single 62 x 29 mm label). Limits: a label whose code is too long to print at a scannable width is refused; at most 5000 labels per PDF; at most 3 text lines on a 5160 label; longer text is cut with an ellipsis. The font is bundled (Go Regular: Latin, Greek and Cyrillic). It is not yet wired to an API route.

## The web app

`web/src/` is laid out as:

- `api/client.ts`: a typed `fetch` wrapper. A failed call throws `ApiError` (the server answered with an error body) or `NetworkError` (no answer).
- `app/`: the router (`routes.tsx`), navigation lists (`navigation.ts`) and theme (`theme.ts`, `ThemeProvider.tsx`).
- `components/`: `Layouts.tsx` (the patron layout and the staff layout, each with a phone tab bar and a desktop side bar), `Shell.tsx`, `Icon.tsx`, `Errors.tsx`.
- `features/`: one folder per area. Today it holds only a placeholder page and the not-found page.
- `styles/`: `tokens.css` (design tokens as CSS custom properties), `fonts.css`, `base.css`. There is no CSS framework.

Fonts (Fraunces, Nunito Sans) are bundled from `@fontsource` packages. The app makes no request to any other host, so it works with no internet connection; `web/src/build.test.ts` fails if the build refers to an external host.

**To add a page:** add a `<Route>` in `app/routes.tsx` under the patron or staff layout, and, if it needs a menu entry, an item in `app/navigation.ts`. The staff tab bar keeps its first four items; the rest go on the "More" page.

**To add a colour scheme:** add a `Scheme` object in `app/theme.ts` with a value for every name in `COLOUR_TOKENS` (the defaults in `tokens.css` are the Hearth scheme); `ThemeProvider` applies it.

## Building and testing

`scripts/check.sh` is the repository's check: in `web/` the typecheck, lint, tests and build, then `gofmt`, `go vet` and `go test ./...`. It needs dependencies installed beforehand and uses no network. Go tests use a real SQLite file in a temporary directory and `httptest`; web tests use Vitest, Testing Library and jsdom.
