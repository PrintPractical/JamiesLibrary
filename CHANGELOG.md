# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Test seams for the test suites: one clock source (`internal/clock`), test hooks compiled only with `-tags testhooks`, an in-process test server and a fake Open Library (`internal/testutil`).
- Project skeleton: a Go server with a health endpoint, the embedded web app, and the repository check.
- One data directory holds everything the library keeps, so a copy of it made while the server is stopped is a complete backup. It is private to its owner, and a second server on the same directory is refused.
- Upgrades keep the data: before the database is changed, a copy of it is saved in `backups/`, and an upgrade that fails changes nothing and says where the copy is. A program older than its data refuses to start rather than damage it. See `docs/data-and-upgrades.md`.
- The web app's frame: patron and staff layouts for phones and desktops, a colour theme, bundled fonts and clear error pages. It makes no request to any other host, so it works with no internet connection.
- Barcode and label printing: Code 128, EAN-13 (from an ISBN-13) and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and a single 62 x 29 mm label (not yet reachable from the web app).
- Architecture and data documentation in `docs/`.
