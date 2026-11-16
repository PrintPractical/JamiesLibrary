# Features

What Jamie's Library does today, by area. Each entry names the requirements it comes from.

## Running and keeping data

- Runs as one program with the web app built in; settings by flag or `JL_*` variable.
- All state is in one data directory, private to its owner; a copy of it taken while the server is stopped is a complete backup (DEPLOY-T3).
- An upgrade first saves a copy of the database in `backups/`, applies all changes or none, and says what to do if it can't; a program older than its data refuses to start (DEPLOY-T4).
- Works with no internet connection: the web app bundles its fonts and scripts (DEPLOY-T5).
- The first administrator is created with a one-time setup code shown only on the server's console, and only until an administrator exists (DEPLOY-3).
- `jamieslibrary admin reset-password USERNAME` recovers a locked-out administrator from the server itself (DEPLOY-4).

## Web app

- Patron and staff layouts that work at phone width and on a desktop, with placeholder pages until each feature is built (UX-1, UX-2).

## Printing

- Code 128, EAN-13 and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and single 62 x 29 mm labels (CARD-1, CAT-7).
