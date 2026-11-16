# Features

What Jamie's Library does today, by area. Each entry names the requirements it comes from.

## Running and keeping data

- Runs as one program with the web app built in; settings by flag or `JL_*` variable.
- All state is in one data directory, private to its owner; a copy of it taken while the server is stopped is a complete backup (DEPLOY-T3).
- An upgrade first saves a copy of the database in `backups/`, applies all changes or none, and says what to do if it can't; a program older than its data refuses to start (DEPLOY-T4).
- Works with no internet connection: the web app bundles its fonts and scripts (DEPLOY-T5).

## Circulation

- Check out a copy to a patron by scanning or typing their card and the copy's barcode (or, by ISBN, the book's), with a due date set by the loan period in the library's time zone; refused when the copy is out, the patron is at their limit or deactivated (CIRC-1, CIRC-4, SCAN-3, SCAN-4). API only so far.
- In ISBN mode, checkout picks the lowest-numbered copy that is in and says which; with none in it says so (CIRC-1, CAT-9).
- Check in by copy barcode or ISBN; with several copies out, choose the loan or scan the patron's card; returned loans stay in history (CIRC-2).
- A copy or book on loan can't be removed from the catalog.

## Web app

- Patron and staff layouts that work at phone width and on a desktop, with placeholder pages until each feature is built (UX-1, UX-2).

## Printing

- Code 128, EAN-13 and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and single 62 x 29 mm labels (CARD-1, CAT-7).
