# Features

What Jamie's Library does today, by area. Each entry names the requirements it comes from.

## Running and keeping data

- Installs with one Docker Compose command on 64-bit x86 or ARM Linux (a Raspberry Pi 4 is enough), or as a single downloaded program; one volume or folder holds everything (DEPLOY-T1, DEPLOY-T6).
- The same functions on a home network or on the internet behind a reverse proxy (DEPLOY-1).
- Runs as one program with the web app built in; settings by flag or `JL_*` variable.
- All state is in one data directory, private to its owner; a copy of it taken while the server is stopped is a complete backup (DEPLOY-T3).
- An upgrade first saves a copy of the database in `backups/`, applies all changes or none, and says what to do if it can't; a program older than its data refuses to start (DEPLOY-T4).
- Serves HTTPS itself: a local certificate authority that each device trusts once, the owner's own certificate, or a reverse proxy; the trust guide at `/trust/` walks a device through it (DEPLOY-T7, SCAN-T2, SEC-T1).
- Works with no internet connection: the web app bundles its fonts and scripts (DEPLOY-T5).
- Written guides for a home or office network and for the public internet, each with installation, configuration, network access, security, and backup and upgrade (DEPLOY-2).
- The first administrator is created with a one-time setup code shown only on the server's console, and only until an administrator exists (DEPLOY-3).
- `jamieslibrary admin reset-password USERNAME` recovers a locked-out administrator from the server itself (DEPLOY-4).

## Circulation

- Check out a copy to a patron by scanning or typing their card and the copy's barcode (or, by ISBN, the book's), with a due date set by the loan period in the library's time zone; refused when the copy is out, the patron is at their limit or deactivated (CIRC-1, CIRC-4, SCAN-3, SCAN-4). API only so far.
- In ISBN mode, checkout picks the lowest-numbered copy that is in and says which; with none in it says so (CIRC-1, CAT-9).
- Check in by copy barcode or ISBN; with several copies out, choose the loan or scan the patron's card; returned loans stay in history (CIRC-2).
- A copy or book on loan can't be removed from the catalog.
- Renew a loan, for a patron or for oneself, up to the renewals the library allows; refused when it is overdue or at the limit, and the reason is given (CIRC-5).
- A loan is overdue once today, in the library's time zone, is after its due date; staff have a list of overdue loans (CIRC-6). Patrons see their own loans and history through the API (PATRON-1).
- Self-checkout: when an administrator switches it on, a patron with the permission can check an available copy out to themselves (CIRC-7). API only so far.
- Staff look up a patron by name or card number, see their loans and history, renew for them and re-issue a lost card, on the Patrons screen (`/desk/patrons`) and the Overdue screen (`/desk/overdue`) (CIRC-3, CIRC-6, CARD-1).

## Accounts and permissions

- Sign in at `/signin`, register at `/register` with an invitation code, change one's own password and sign out at `/account`; a password reset by an administrator forces a new password at the next sign-in. Signing out clears the app's cached data (ACCESS-2, ACCESS-5, ACCESS-6, ACCESS-7).
- Sessions end after a time unused that an administrator sets, 8 hours to begin with (SEC-T1, ADMIN-1). Repeated wrong sign-ins block an address for 15 minutes without locking any account.
- Nothing is shown to signed-out visitors unless an administrator turns the public catalog on (ACCESS-1).
- 16 separate permissions, grouped into the roles patron, librarian and administrator, which are starting sets: an administrator can switch single permissions for a person, and the user list marks anyone who differs from their role (ROLE-1, ROLE-2, ROLE-3, ADMIN-2). A person is never given more than the administrator granting it holds.
- Administrators list users, change roles and permissions, deactivate and reactivate accounts (which ends their sessions) and reset passwords, with a temporary password shown once; there is no email in this version (ADMIN-2, ACCESS-7). API only so far.
- Invitation codes, one use each, for a role; administrators create, list and revoke them (ACCESS-2, ACCESS-3). API only so far, apart from creating them in first-run setup.
- Navigation and screens follow the person's permissions: patrons and staff see different layouts (UX-2). The patron and staff pages for browsing, borrowing and people are placeholders until their features are built.

## Library cards

- Every patron has a 12-digit card number, shown as a Code 128 barcode and a QR code; staff can re-issue a lost card, which replaces the number (CARD-1). The card never shares a code with a copy (CAT-3).

## Catalog

- Books with title, authors, description, ISBN (10 or 13, checked and stored as 13) and cover; several numbered copies of each, each with a condition and a named location; search by title, author or ISBN with a location filter; removing a book or copy keeps its history (CAT-1, CAT-2, CAT-3, CAT-5, CAT-8). Browsing and search have a screen (`/browse`); adding and managing books does not yet.
- Browse, search and book pages: the Browse screen lists books with their availability, searches by title, author or whole ISBN and filters by location; a book's page shows the cover, average rating, each copy's location and due date, and the description (CAT-2, CAT-4, CAT-8). See [Finding books](docs/guide-browsing.md).
- Look up a book's details and cover by ISBN from Open Library, with a clear message when it can't be reached; covers are kept on the installation (SCAN-2, CAT-T1). API only so far.
- Ratings of 1 to 5 and reviews of up to 2,000 characters, one per person per book; librarians can remove a review, and its author can't repost it (CAT-6). Written and removed on the book's page.
- Optional public catalog: with the setting on, anyone can search and see availability and the average rating, never who has a book or where each copy is, on the same Browse and book screens, and a visitor is asked to sign in to read reviews (ACCESS-4).
- Copy labels as PDF (Avery 5160, Avery L7160 or a single 62 x 29 mm label, up to 5,000 labels), with contents that suit the library's way of identifying copies (CAT-7, CAT-9). API only so far.

## First-run setup

- A guided setup at `/setup`: the administrator's account with the console's one-time code, then name, logo and colours, borrowing rules and time zone (filled in from the browser), and invitations. Steps after the first can be skipped and changed later in Settings (DEPLOY-3, ADMIN-1, CIRC-4).

## Reports and the activity log

- Statistics screen (`/admin/statistics`): numbers of books, copies, active patrons, loans out now and overdue loans, and a count of checkouts and returns over a chosen date range (the last 30 days to begin with) of up to 5 years, in the library's time zone, with no patron names (ADMIN-3). Also in the API for people who may view reports.
- Activity log screen (`/admin/activity`): what administrators did (setup, settings with old and new values, roles, permissions, deactivation, password resets, invitations, card re-issues, removed reviews), newest first, 50 to a page, in the library's time zone, with who did it and to whom (ADMIN-4).

## Web app

- The Settings screen, for administrators (`/admin/settings`): library name, logo (JPEG, PNG or WebP up to 2 MB; refusals are shown) and colour scheme with a custom accent, a live preview and the server's verdict on readability (ADMIN-1); loan period, items out at once, renewals and time zone (CIRC-4); the sign-out time for unused sessions; switches for self-checkout and the public catalog, each saying what it does (CIRC-7, ACCESS-4); how copies are identified, where switching asks first and explains what it does (CAT-9); and, with the library's own certificate authority, its root fingerprint to check a device's own display against (DEPLOY-T7). See the [administrator's guide](docs/guide-admin.md).
- Patron and staff layouts that work at phone width and on a desktop, with placeholder pages until each feature is built (UX-1, UX-2).
- One scan field for every screen that reads a code: a hand scanner or typing, and the phone's camera where the page is served securely, with a link to the trust guide where it isn't (SCAN-1, SCAN-T2).

## Printing

- Code 128, EAN-13 and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and single 62 x 29 mm labels (CARD-1, CAT-7).
