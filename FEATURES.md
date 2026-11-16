# Features

What Jamie's Library does today, by area. Each entry names the requirements it comes from.

## Running and keeping data

- Installs with one Docker Compose command on 64-bit x86 or ARM Linux (a Raspberry Pi 4 is enough), or as a single downloaded program; one volume or folder holds everything (DEPLOY-T1, DEPLOY-T6).
- The same functions on a home network or on the internet behind a reverse proxy (DEPLOY-1).
- Runs as one program with the web app built in; settings by flag or `JL_*` variable.
- All state is in one data directory, private to its owner; a copy of it taken while the server is stopped is a complete backup (DEPLOY-T3).
- An upgrade first saves a copy of the database in `backups/`, applies all changes or none, and says what to do if it can't; a program older than its data refuses to start (DEPLOY-T4).
- Serves HTTPS itself: a local certificate authority that each device trusts once, the owner's own certificate, or a reverse proxy; the trust guide at `/trust/` walks a device through it (DEPLOY-T7, SCAN-T2, SEC-T1). The Mac steps avoid double-clicking the certificate (Keychain error -25294): import it with Keychain Access › File › Import Items…, choose the keychain, compare the fingerprint, then Always Trust (DEPLOY-T7).
- Works with no internet connection: the web app bundles its fonts and scripts (DEPLOY-T5).
- Written guides for a home or office network and for the public internet, each with installation, configuration, network access, security, and backup and upgrade (DEPLOY-2).
- The first administrator is created with a one-time setup code shown only on the server's console, and only until an administrator exists (DEPLOY-3).
- `jamieslibrary admin reset-password USERNAME` (with `--make-admin` to restore the administrator role) recovers a locked-out administrator from the server itself, also through `docker compose exec`; it prints a one-time temporary password and is recorded in the activity log as the server console (DEPLOY-4). See [accounts](docs/accounts.md#locked-out-recovering-the-only-administrator).

## Circulation

- Check out: on the Check out screen (`/desk/checkout`) a librarian scans or types a patron's card, then each copy's barcode (or, in ISBN mode, the book's); each scan is lent at once with its copy number and a due date set by the loan period in the library's time zone, and the screen shows how many the patron has out and what they had before (CIRC-1, CIRC-4, SCAN-3, SCAN-4). Refused, with what to do, when the card or code is unknown, the copy is out (and with whom), the patron is at their limit or deactivated. See [Working the desk](docs/guide-desk.md).
- In ISBN mode, checkout picks the lowest-numbered copy that is in and says which; with none in it says so (CIRC-1, CAT-9).
- Check in on the Check in screen (`/desk/checkin`): scan copy barcodes or ISBNs one after another with no patron chosen; the screen says where to put the book back and how late it was, and keeps a list of the session. With several copies of a book out, the librarian scans the patron's card or picks the loan; an ISBN with nothing out says so. Returned loans stay in history (CIRC-2).
- A copy or book on loan can't be removed from the catalog.
- Renew a loan, for a patron from their Home screen or for them from the patron's page at the desk, by the loan period from the current due date, up to the renewals the library allows; refused when it is overdue or at the limit, and the reason is given (CIRC-5).
- A loan is overdue once today, in the library's time zone, is after its due date. The Overdue screen (`/desk/overdue`) lists overdue loans with the patron and days late, longest first (CIRC-6).
- Staff look up a patron by name or card number on the Patrons screen (`/desk/patrons`) and open their page for current loans, overdue badges and full borrowing history, with renewing (CIRC-3, CIRC-5).
- Re-issue a lost card from the patron's page: the old number stops identifying anyone at once, and the new one is shown once (CARD-1).
- Patron Home (`/`): the date, how many books are out and how many overdue, each loan with its due date (words and icon, not colour alone) and a Renew button or the reason it can't be renewed; and a Borrowing history page (`/loans`), a page at a time (PATRON-1, CIRC-5, UX-2). See [Borrowing books](docs/guide-patron.md).
- Self-checkout: when an administrator switches it on, a patron with the permission scans or types a book's code on the Check out a book page (`/checkout`) and it is lent to them, with every refusal explained; patrons can't return books themselves (CIRC-7).

## Accounts and permissions

- Sign in at `/signin`, register at `/register` with an invitation code, change one's own password and sign out at `/account`; a password reset by an administrator forces a new password at the next sign-in. Signing out clears the app's cached data (ACCESS-2, ACCESS-5, ACCESS-6, ACCESS-7).
- Sessions end after a time unused that an administrator sets, 8 hours to begin with (SEC-T1, ADMIN-1). Repeated wrong sign-ins block an address for 15 minutes without locking any account.
- Nothing is shown to signed-out visitors unless an administrator turns the public catalog on (ACCESS-1).
- 16 separate permissions, grouped into the roles patron, librarian and administrator, which are starting sets: an administrator can switch single permissions for a person, and the user list marks anyone who differs from their role (ROLE-1, ROLE-2, ROLE-3, ADMIN-2). A person is never given more than the administrator granting it holds.
- On **People & invites** (`/admin/people`), people who may manage users list everyone and open a person's page to set their role, switch single permissions, deactivate and reactivate them (which ends their sessions) and reset their password, with a temporary password shown once; there is no email in this version (ADMIN-2, ACCESS-7). Nobody can give or change more than they hold, and the last administrator can't be removed (ROLE-1, ROLE-2, ROLE-3, ADMIN-2). See [accounts](docs/accounts.md).
- Invitation codes, one use each, for a role; people who may manage invitations make, see and revoke them on **People & invites**, and a code that gives permissions the viewer lacks is hidden from them (ACCESS-2, ACCESS-3).
- The staff home, the Desk (`/desk`), where librarians and administrators land: large Check out and Check in links, today's overdue count (opening the Overdue list; `Nothing overdue`, `1 loan overdue` or `N loans overdue`, with a retry if it can't be counted), a Find a patron box that opens Patrons with the search done, and Add a book. Each card shows only for someone with its permission (`circ.desk`, `patrons.lookup`, `catalog.manage`); a staff member with none of them is told there is nothing for them at the desk and offered their Admin pages (UX-2, CIRC-3, CIRC-6). See [Working the desk](docs/guide-desk.md#the-desk).
- Navigation and screens follow the person's permissions: patrons and staff see different layouts (UX-2).

## Library cards

- Every patron has a 12-digit card number, shown on My card (`/card`) with their name and the library's logo as a Code 128 barcode and a QR code; staff can re-issue a lost card, which replaces the number (CARD-1). The card never shares a code with a copy (CAT-3).

## Catalog

- Books with title, authors, description, ISBN (10 or 13, checked and stored as 13) and cover; several numbered copies of each, each with a condition and a named location; search by title, author or ISBN with a location filter; removing a book or copy keeps its history (CAT-1, CAT-2, CAT-3, CAT-5, CAT-8). Browsing and search have a screen (`/browse`). Librarians add a book, or more copies of one already in the catalog, on the Add a book screen (`/catalog/new`), and change or remove books and copies on the Catalog screens (`/catalog`). See [Looking after the catalog](docs/guide-catalog.md).
- Browse, search and book pages: the Browse screen lists books with their availability, searches by title, author or whole ISBN and filters by location; a book's page shows the cover, average rating, each copy's location and due date, and the description (CAT-2, CAT-4, CAT-8). See [Finding books](docs/guide-browsing.md).
- Look up a book's details and cover by ISBN from Open Library, with a clear message when it can't be reached; covers are kept on the installation (SCAN-2, CAT-T1). Used by the Add a book screen: scan or type an ISBN, check the details found, and add the book.
- Ratings of 1 to 5 and reviews of up to 2,000 characters, one per person per book; librarians can remove a review, and its author can't repost it (CAT-6). Written and removed on the book's page.
- Optional public catalog: with the setting on, anyone can search and see availability and the average rating, never who has a book or where each copy is, on the same Browse and book screens, and a visitor is asked to sign in to read reviews (ACCESS-4).
- Librarians change a book's details, ISBN and cover, and edit, add or remove its copies (location and condition); a copy or book on loan can't be removed, and a removed copy's barcode can be used again (CAT-1, CAT-3, CAT-5).
- Named locations: add, rename, and delete an empty one, or move its copies to another location and delete it in one step; a copy's location can also be made while adding it (CAT-8). A new library has one, Main shelf.
- Add a book by scanning or typing its ISBN, from a handheld scanner, the camera or the keyboard; the details are checked before anything is saved, and with no internet the form stays open to fill in by hand (SCAN-1, SCAN-2, SCAN-3, SCAN-T2).
- Copies are identified by the book's ISBN or by a barcode of their own (a generated label, or a sticker already on the item); a book with no ISBN always has its own (CAT-3, CAT-9).
- Copy labels as PDF (Avery 5160, Avery L7160 or a single 62 x 29 mm label, up to 5,000 labels), with contents that suit the library's way of identifying copies (CAT-7, CAT-9). The Print labels screen (`/catalog/labels`) picks copies from any books, and the Add a book screen offers to print the labels of the copies just added.

## First-run setup

- A guided setup at `/setup`: the administrator's account with the console's one-time code, then name, logo and colours, borrowing rules and time zone (filled in from the browser), and invitations. Steps after the first can be skipped and changed later in Settings (DEPLOY-3, ADMIN-1, CIRC-4).
- While no administrator exists, the sign-in page and `/register` send visitors to `/setup`, and the console prints the setup page address; once one exists they behave as before (DEPLOY-3).

## Reports and the activity log

- Statistics screen (`/admin/statistics`): numbers of books, copies, active patrons, loans out now and overdue loans, and a count of checkouts and returns over a chosen date range (the last 30 days to begin with) of up to 5 years, in the library's time zone, with no patron names (ADMIN-3). Also in the API for people who may view reports.
- Activity log screen (`/admin/activity`): what administrators did (setup, settings with old and new values, roles, permissions, deactivation, password resets, invitations, card re-issues, removed reviews), newest first, 50 to a page, in the library's time zone, with who did it and to whom (ADMIN-4).

## Web app

- The Settings screen, for administrators (`/admin/settings`): library name, logo (JPEG, PNG or WebP up to 2 MB; refusals are shown) and colour scheme with a custom accent, a live preview and the server's verdict on readability (ADMIN-1); loan period, items out at once, renewals and time zone (CIRC-4); the sign-out time for unused sessions; switches for self-checkout and the public catalog, each saying what it does (CIRC-7, ACCESS-4); how copies are identified, where switching asks first and explains what it does (CAT-9); and, with the library's own certificate authority, its root fingerprint to check a device's own display against (DEPLOY-T7). See the [administrator's guide](docs/guide-admin.md).
- Patron and staff layouts that work at phone width and on a desktop (UX-1, UX-2).
- Staff on the patron side see a single **Desk** link back to `/desk`, in the phone's top bar and at the foot of the desktop side bar; patrons and visitors never see it (UX-2). See [Working the desk](docs/guide-desk.md#the-desk).
- One scan field for every screen that reads a code: a hand scanner or typing, and the phone's camera where the page is served securely, with a link to the trust guide where it isn't (SCAN-1, SCAN-T2).

## Printing

- Code 128, EAN-13 and QR codes as SVG, and PDF label sheets for Avery 5160, Avery L7160 and single 62 x 29 mm labels (CARD-1, CAT-7).
