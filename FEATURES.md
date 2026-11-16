# Features

What Jamie's Library does today, by area. Each entry names the requirements it comes from.

## Running and keeping data

- Installs with one Docker Compose command on 64-bit x86 or ARM Linux (a Raspberry Pi 4 is enough), or as a single downloaded program; one volume or folder holds everything (DEPLOY-T1, DEPLOY-T6).
- The same functions on a home network or on the internet behind a reverse proxy (DEPLOY-1).
- Runs as one program with the web app built in; settings by flag or `JL_*` variable.
- All state is in one data directory, private to its owner; a copy of it taken while the server is stopped is a complete backup (DEPLOY-T3).
- An upgrade first saves a copy of the database in `backups/`, applies all changes or none, and says what to do if it can't; a program older than its data refuses to start (DEPLOY-T4).
- Works with no internet connection: the web app bundles its fonts and scripts (DEPLOY-T5).
- Written guides for a home or office network and for the public internet, each with installation, configuration, network access, security, and backup and upgrade (DEPLOY-2).
- The first administrator is created with a one-time setup code shown only on the server's console, and only until an administrator exists (DEPLOY-3).
- `jamieslibrary admin reset-password USERNAME` recovers a locked-out administrator from the server itself (DEPLOY-4).

## Circulation

- Check out a copy to a patron by scanning or typing their card and the copy's barcode (or, by ISBN, the book's), with a due date set by the loan period in the library's time zone; refused when the copy is out, the patron is at their limit or deactivated (CIRC-1, CIRC-4, SCAN-3, SCAN-4). API only so far.
- In ISBN mode, checkout picks the lowest-numbered copy that is in and says which; with none in it says so (CIRC-1, CAT-9).
- Check in by copy barcode or ISBN; with several copies out, choose the loan or scan the patron's card; returned loans stay in history (CIRC-2).
- A copy or book on loan can't be removed from the catalog.

## Reports

- Library statistics (books, copies, active patrons, current and overdue loans) and a count of checkouts and returns over a chosen date range of up to 5 years, in the library's time zone, with no patron names (ADMIN-3). API only so far.

## Web app

- The Settings screen, for administrators (`/admin/settings`): library name, logo (JPEG, PNG or WebP up to 2 MB; refusals are shown) and colour scheme with a custom accent, a live preview and the server's verdict on readability (ADMIN-1); loan period, items out at once, renewals and time zone (CIRC-4); the sign-out time for unused sessions; switches for self-checkout and the public catalog, each saying what it does (CIRC-7, ACCESS-4); how copies are identified, where switching asks first and explains what it does (CAT-9); and, with the library's own certificate authority, its root fingerprint to check a device's own display against.
- Patron and staff layouts that work at phone width and on a desktop, with placeholder pages until each feature is built (UX-1, UX-2).

## Printing

- Code 128, EAN-13 and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and single 62 x 29 mm labels (CARD-1, CAT-7).
