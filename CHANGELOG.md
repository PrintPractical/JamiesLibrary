# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- The administrative activity log: `GET /api/admin/activity` (for people who may view activity) lists what administrators did, newest first, with who did it and when, 50 per page by default and 100 at most (`page`, `per_page`). An action that is refused or rolled back is never logged, and codes and passwords never are. Nothing writes to it yet; settings, accounts and invitations will.
- The trust guide at `/trust/` on the plain-HTTP port: steps for iPhone and iPad, Android and computers (the device's own first, the others one tap away), the root certificate's fingerprint to compare, what the device will trust, and a button to open the library over HTTPS. With the owner's own certificate it says no step is needed. It uses the bundled fonts and loads nothing from another host.
- `JL_BIND_ADDRESS` (`--bind`) sets the IP address the server listens on, for both listeners; the default is every interface, as before. A bad value stops start-up and names the setting. In `JL_HTTP_ONLY` mode without it, the server logs a warning that the app is served over plain HTTP on every interface.
- Foundations for the catalog, not yet on a screen or in the API: books with ordered authors, ISBN-10 and ISBN-13 entry stored as ISBN-13, copies numbered within each book, named locations (a new library has "Main shelf"), and search by title, author or ISBN. Removing a book or copy keeps its history.
- Foundations for copy barcodes, not yet on a screen or in the API: each copy can carry its own code (typed from a sticker, or generated like `C0000012`), and a code can never be shared by two copies or match a library card number. A library can identify copies by ISBN (the default) or by their own barcodes; switching to own barcodes gives every copy without one a code, and switching back keeps them.
- Foundations for library settings, not yet on a screen or in the API: the settings with their defaults and limits (name, colour scheme, accent colour, time zone, loan period, loans and renewals allowed, self-checkout, public catalog, copy identification, sign-out time), and a readability check that refuses an accent colour whose text would be hard to read and suggests a darker one.
- Test seams for the test suites: one clock source (`internal/clock`), test hooks compiled only with `-tags testhooks`, an in-process test server and a fake Open Library (`internal/testutil`).
- Project skeleton: a Go server with a health endpoint, the embedded web app, and the repository check.
- One data directory holds everything the library keeps, so a copy of it made while the server is stopped is a complete backup. It is private to its owner, and a second server on the same directory is refused.
- Upgrades keep the data: before the database is changed, a copy of it is saved in `backups/`, and an upgrade that fails changes nothing and says where the copy is. A program older than its data refuses to start rather than damage it. See `docs/data-and-upgrades.md`.
- The web app's frame: patron and staff layouts for phones and desktops, a colour theme, bundled fonts and clear error pages. It makes no request to any other host, so it works with no internet connection.
- Barcode and label printing: Code 128, EAN-13 (from an ISBN-13) and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and a single 62 x 29 mm label (not yet reachable from the web app).
- Architecture and data documentation in `docs/`.
