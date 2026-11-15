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
internal/catalog/         books, authors, copies, locations, scannable codes, search, copy-ID mode (see below)
internal/settings/        the library's settings, colour schemes, the contrast check (see below)
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

## The catalog

None of the catalog, the settings or the barcode package is wired to an API route or a screen yet: they are the server's internal building blocks, used by their tests.

`internal/catalog` owns books, authors, copies, locations and every scannable code. Its tables are created by `0001_catalog.sql` and `0003_copies.sql` (`0002_settings.sql` creates the settings table) in `internal/store/migrations/`.

| table | holds |
|---|---|
| `books` | id, `item_type` (only `book`), title, description, `isbn13`, cover file name, `search_text`, `created_at`, `removed_at` |
| `book_authors` | (book_id, position, name): authors in the order entered |
| `copies` | id, book_id, `number`, condition, location_id, `created_at`, `removed_at`; (book_id, number) is unique |
| `locations` | id, name, `name_key` (the name lower-cased, unique) |
| `identifiers` | every scannable code: `code` (unique), `kind` (`card` or `copy`), and `user_id` or `copy_id` |

**Removal is soft.** Removing a book or copy sets `removed_at`; the row stays so loans and reports keep their history. A removed book or copy no longer appears in lookups, search or scanning. Removing a book removes its copies, and both are refused while a copy is on loan. A copy's number is never reused, so the next copy of a book is numbered after the highest number it has ever had.

**Loans.** Loans belong to circulation, which is not built yet. The catalog asks a small `Loans` interface (`LoanedCopies`, `OnLoan`); until circulation provides it, `NoLoans` answers that nothing is on loan.

**Limits** (counted in characters; control and text-direction characters are refused; text is trimmed): title 500, author name 200, 50 authors, description 10,000, location name 100. A book needs a title and at least one author.

**ISBNs.** `ParseISBN` accepts ISBN-10 or ISBN-13, with or without hyphens or spaces (a final `X` only on an ISBN-10), checks the check digit, and stores ISBN-13; an ISBN-13 must start with 978 or 979. Two books in the catalog cannot share an ISBN: the second is refused with a `DuplicateISBN` error that carries the existing book's ID, so a screen can offer that book. A removed book gives its ISBN back.

**Search.** `Search(query, locationID, page)`:

- an empty query lists every book;
- a query that is a valid ISBN matches that ISBN exactly;
- otherwise the query is split into words (at spaces and periods, so "J.R.R. Tolkien" finds "J. R. R. Tolkien"), and every word must appear, ignoring case, in the title or an author's name (these are held lower-cased in `books.search_text`). `%`, `_` and `\` match themselves;
- a location filter keeps books with a copy there;
- the query is at most 200 characters and 10 words; results are in title order, 50 to a page, and carry the total count.

**Locations.** A new library has one, "Main shelf". Names are unique ignoring case; renaming changes it for every copy there. A location that holds copies cannot be deleted: the error says how many, and `MoveCopies` moves them all to another location. A copy's location is checked by the service rather than by a foreign key, so a removed copy never blocks deleting a location.

**Copy conditions** are `new`, `good` (the default), `fair`, `worn`, `poor` and `damaged`.

### Scannable codes

Card numbers and copy codes live in one table, `identifiers`, under one unique index on `code`, so a code can never identify both a patron and a copy. A copy has at most one code.

- **Normalising.** Every code that is typed or scanned goes through `NormaliseCode` before it is stored or looked up: trimmed, upper-cased, and 1 to 32 printable ASCII characters with no spaces.
- **Copy codes** are either a sticker someone entered (normalised) or generated: `C` and a seven-digit zero-padded number, such as `C0000012`. The number follows the copy's internal ID; a generated code that a typed-in sticker already holds is skipped.
- **Refusals.** A sticker that equals a card number is refused ("That code is a library card number…"), and one another copy holds is refused ("Another copy already has this barcode…"). Both are 409 conflicts on the `sticker` field.
- **Freeing.** Removing a copy or book deletes its identifier rows, so the sticker can be used again.
- **Looking up.** `LookupCode` finds the live copy a code names; a card number, an unknown code or an invalid code finds nothing.
- **Cards.** The table and its `card` kind are in place, and a card number can never be used as a copy code. Issuing card numbers comes with patron accounts and is not documented here yet.

### Copy-ID mode

The mode is the `copy_id_mode` setting (`isbn`, the default, or `own`).

- **A copy gets a code** when the mode is `own`, or when its book has no ISBN, or when a sticker is entered. In `isbn` mode a copy of a book with an ISBN has none, and is identified by the book's ISBN.
- **Switching to `own`** happens in one transaction: the setting changes and every live copy without a code gets a generated one. Copies and loans are untouched.
- **Switching back to `isbn`** changes only the setting. Every copy keeps its code, and scanning it still finds that copy.
- A copy's code is changed by updating the copy with a new sticker; leaving the sticker empty keeps the current code.

## Settings

`internal/settings` owns the `settings` table, which always has exactly one row. It checks every value before storing it and keeps the row in memory, so reading a setting costs no query; each change refreshes the copy in memory. Today's date for due dates is `Today()`: the date in the library's time zone, taken from `internal/clock`.

| setting | default | accepted |
|---|---|---|
| library name | Jamie's Library | 1 to 80 characters, no control characters |
| logo | none | a stored image's file name |
| colour scheme | `hearth` | `hearth`, `reading-room`, `inkwell`, `heather` |
| accent colour | none (the scheme's own) | `#RRGGBB`, and it must pass the contrast rule |
| time zone | `UTC` | an IANA name such as `Europe/London`; `Local` is refused; tz data is built in |
| loan period | 14 days | 1 to 365 |
| most loans per patron | 5 | 1 to 100 |
| most renewals | 2 | 0 to 20 |
| self-checkout | off | on or off |
| public catalog | off | on or off |
| copy-ID mode | `isbn` | `isbn` or `own` |
| sign out after idle | 480 minutes | 15 to 43,200 (30 days) |

Each setter returns an error on the offending field with a sentence that says what is accepted.

### The contrast rule

The scheme and the accent are saved together by `SetBranding`, because whether an accent is readable depends on the scheme. An accent colour must reach a WCAG 2.x contrast ratio of at least 4.5 : 1 in two tests:

- **on accent:** the scheme's on-primary text colour drawn on the accent (button labels);
- **as text:** the accent drawn on the scheme's page background (links).

If either fails, nothing is stored and the error names the failed test with its measured ratio, and suggests a darker shade of the same hue that passes both. The rule is the one function `CheckAccent`; the web app asks the server rather than repeating it. An accent is stored upper-cased. The four schemes are Go data in `schemes.go`; `Scheme.Tokens` turns a scheme and accent into the CSS custom properties the web app applies (the accent's hover and soft shades are derived from it).

## Barcodes and labels

`internal/barcode` is pure rendering and knows nothing of the database. It makes SVG codes and PDF label sheets.

**Codes.** Code 128 (1 to 32 printable ASCII characters, used for copy codes and card numbers), EAN-13 (from an ISBN-13, checked, with or without hyphens) and QR (up to 1024 bytes). The SVG has a quiet zone around the code and a `viewBox` but no fixed size, so the viewer scales it. Invalid input is an error wrapping `barcode.ErrInvalid` and produces no image.

**Layouts.** A layout gives the page size, grid and label size in millimetres; labels fill each row left to right and each page top to bottom. `LayoutByName` finds them by name.

| name | page | grid | label |
|---|---|---|---|
| `avery5160` | US Letter | 3 x 10 (30 a sheet) | 2.625 x 1 in |
| `averyl7160` | A4 | 3 x 7 (21 a sheet) | 63.5 x 38.1 mm |
| `single` | 62 x 29 mm, one label per page | 1 x 1 | 62 x 29 mm |

A PDF is used rather than printing from a browser because only a PDF puts the grid exactly where the sheet's labels are.

**A label** is a code drawn as bars with its text beneath, then lines of text. The caller chooses the contents: in own-barcode mode a label carries the copy's Code 128 code and its book's title; in by-ISBN mode the book's ISBN as EAN-13, with the title, copy number and location. Limits:

- the bars are drawn as wide as the label allows, at most 0.5 mm per module; a code that would need modules narrower than 0.25 mm is refused, since it could not be scanned reliably;
- a line wider than the label is cut with an ellipsis, never overflowing into the next label; a character the bundled font (Go Regular: Latin, Greek, Cyrillic) cannot draw is shown as `?`;
- text lines that fit under the code: 3 on an Avery 5160 label, 6 on an Avery L7160 and 4 on a single label; more is refused with an error that says how many fit;
- at most 5000 labels in one PDF; more are refused, and everything is validated before anything is drawn.

The PDF has a fixed creation date, so the same labels produce the same file.

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
