# Administrator's guide: first-run setup, settings and reports

This guide is for the person who runs the library: setting it up the first time, choosing how it looks and how borrowing works, and reading its statistics and activity log. To install the program, start with [a home or office network](install-lan.md) or [the public internet](install-public.md).

Settings, statistics and the activity log are done in the web app, signed in as an administrator. People, roles and invitations have their own guide: [accounts, permissions, invitations and recovery](accounts.md). Some other screens named below (browsing the catalog, patrons' own pages) are not built yet; where that matters, the guide says so. Each part of the Settings screen, and each of the Statistics and Activity log screens, is also open to anyone given the matching permission (`settings.manage`, `reports.view`, `activity.view`); an administrator has them all. Anyone without the permission doesn't see the entry in the side bar.

- [First-run setup](#first-run-setup)
- [Settings](#settings)
  - [Name, logo and colours](#name-logo-and-colours)
  - [Borrowing rules and time zone](#borrowing-rules-and-time-zone)
  - [Signing in: how long a session lasts](#signing-in-how-long-a-session-lasts)
  - [What patrons can do: self-checkout and the public catalog](#what-patrons-can-do-self-checkout-and-the-public-catalog)
  - [How copies are identified](#how-copies-are-identified)
  - [Secure connection: the HTTPS fingerprint](#secure-connection-the-https-fingerprint)
- [Statistics and the circulation report](#statistics-and-the-circulation-report)
- [The activity log](#the-activity-log)

## First-run setup

A new library has no accounts. The server makes the first administrator only for someone who can see the server itself, using a one-time **setup code**.

1. Start the server. While no administrator exists it prints, among its first lines, the code on a line of its own:

   ```
   No administrator exists yet. To create the first one, open the app in a browser and enter this one-time code.
   On a phone or another computer, first follow the trust guide: http://localhost:8080/trust/
   SETUP CODE: R0C8-G5GT-X0Z4-NPJY
   ```

   With Docker, read it from the container's log: `docker compose -p jamieslibrary -f deploy/compose.lan.yml logs | grep "SETUP CODE"`. The code is new at every start, is kept only in memory and is never written to a file. After a restart, use the newest one.

2. Open the library in a browser (for example `https://THIS-MACHINE:8443`; on a phone or another computer do [the trust step](install-lan.md#the-trust-step-once-per-phone-or-computer) first). Anyone who opens `/setup` sees step 1 until an administrator exists; after that `/setup` sends visitors to the sign-in page.

3. **Step 1, Create the administrator.** Enter the setup code, your name (as the library will show it), a username, and a password of at least 10 characters. Choose **Create account and continue**. You're signed in at once. A wrong code is refused; ten wrong codes from one address block it for 15 minutes.

4. **Step 2, Make it yours.** The library's name, logo and colours, with a live preview. See [Name, logo and colours](#name-logo-and-colours).

5. **Step 3, How borrowing works.** The loan period, how many books a person can have out, how many renewals, and the time zone. The time zone box is filled in with the one your browser is in; check it. See [Borrowing rules and time zone](#borrowing-rules-and-time-zone).

6. **Step 4, Invite people.** Choose a role and **Create invitation** for each person you want to bring in. Each code works once. Send it with the address of the library followed by `/register`, and hand it over privately (not in a group chat or on a shared screen): whoever holds a librarian or administrator code can register with that role's permissions. Later, make, see and revoke codes on **People & invites** (see [Inviting people](accounts.md#inviting-people)); revoke any code that goes unused.

Steps 2 and 3 can be skipped with **Skip**, and step 4 with **Skip and finish**; and everything in them can be changed later in Settings. If you close the browser part-way, sign in and open **Settings**; the library already works with its defaults.

The first administrator is the only account made this way. If you are ever locked out, recover from the server with `jamieslibrary admin reset-password USERNAME` (see [Locked out: recovering the only administrator](accounts.md#locked-out-recovering-the-only-administrator)).

## Settings

Open **Settings** in the side bar (`/admin/settings`). Each section is saved on its own, and everyone sees a change straight away.

| Setting | New library | Allowed |
|---|---|---|
| Library name | Jamie's Library | 1 to 80 characters |
| Logo | the default logo | JPEG, PNG or WebP, up to 2 MB |
| Colour scheme | Hearth | Hearth, Reading Room, Inkwell, Heather |
| Accent colour | none (the scheme's own) | `#RRGGBB` that passes the readability check |
| Loan period | 14 days | 1 to 365 |
| Books out at once | 5 | 1 to 100 |
| Renewals | 2 | 0 to 20 (0 means never) |
| Time zone | UTC until you set it | an IANA name such as `Europe/London` |
| Sign out after unused | 480 minutes (8 hours) | 15 to 43200 minutes (30 days) |
| Self-checkout | off | on or off |
| Public catalog | off | on or off |
| How copies are identified | by ISBN | by ISBN, or by own barcode |

A value outside its range is refused with a message that says the range, and nothing changes.

### Name, logo and colours

The name is shown on every screen, on the sign-in page and on library cards. The logo is shown beside it, and to anyone, signed in or not, because the sign-in page shows it.

**Logo.** Choose **Upload** and pick a JPEG, PNG or WebP file of at most 2 MB. A larger file, or one that isn't one of those kinds of image (whatever its name says), is refused with a message, and the current logo stays. **Remove** goes back to the default logo.

**Colour scheme.** Pick one of the four presets; the preview beside the form changes at once. Everyone sees the new scheme the next time a page loads, once you save.

**Accent colour (optional).** To use a colour of your own for buttons and links, type it as `#` and six hex digits, for example `#8A5A12`. **Reset** goes back to the scheme's own colour.

**The contrast rule.** Text has to stay readable. The library measures the colour against the WCAG AA standard of **4.5 : 1** in two ways: the text shown *on* the accent (button labels) and the accent used *as* text or links on the scheme's page background. An accent that fails either is refused, the scheme is left as it was, and you are shown the ratios and a darker colour that passes, with a button to use it. For example, a light yellow gives:

```
Text would be hard to read on this colour: text on #FFEE00 (such as button labels) has a contrast of 1.1 : 1; #FFEE00 as text on the Hearth background has a contrast of 1.1 : 1, and needs at least 4.5 : 1. Try a darker shade, such as #756D00. Your scheme hasn't changed.
```

All four preset schemes pass this rule already.

### Borrowing rules and time zone

- **Loan period** is how many days a book may be kept. A new loan is due that many days after today.
- **Books out at once** is the most one person can have on loan. A checkout over the limit is refused at the desk with a message.
- **Renewals** is how many times one loan can be renewed. Each renewal adds one loan period to the current due date. A loan that is already overdue can't be renewed.
- **Time zone** decides where "today" is. Due dates are calendar dates, not times: a book due on the 14th is on time until midnight on the 14th in this zone and overdue from the start of the 15th. Choose it from the list or type the name; a name the server doesn't know is refused (`"Mars/Base" is not a time zone this server knows. Choose one such as Europe/London or America/Chicago.`). The reports and the times in the activity log use it as well.

A change to the rules applies to loans made from then on; the due dates of loans already out aren't rewritten. A change of time zone applies at once to what counts as overdue.

### Signing in: how long a session lasts

**Sign out after (minutes unused)** is how long someone can leave the library untouched before they must sign in again, from 15 minutes to 30 days (43200). It applies to everyone, including people already signed in. A value outside that range is refused: `The idle time before signing out must be between 15 and 43200 minutes.` A shared desk computer wants a short time; a patron's own phone can have a long one.

### What patrons can do: self-checkout and the public catalog

Each of these switches saves as soon as you flip it, and says in a line what it does.

The screens patrons would use for these (browsing, the book page, My books, checking out) are not built yet, so today the switches change what the server allows through its API:

- **Self-checkout** lets a patron who has the self-checkout permission check an available copy out to themselves, by its code. While it is off, self-checkout is refused. Patrons return books only through a librarian at the desk, and the desk is unaffected either way.
- **Public catalog** lets anyone, signed in or not, search the books through the server's public catalog routes and see each book's title, authors, ISBN, description, cover, how many copies are available and the average rating. They never see patrons, who has a book, or loan history; reviews and their writers' names are for signed-in users only. While it is off those routes answer "not found" and the library is private: a visitor who isn't signed in is sent to the sign-in page.

### How copies are identified

A library either identifies copies **by ISBN** (the default: copies of a book share its ISBN barcode, and scanning an ISBN checks out the lowest-numbered copy that is in) or **by own barcode** (each copy carries its own sticker, and that is what is scanned; scanning an ISBN no longer checks a book out). A book without an ISBN always uses its copies' own barcodes.

Switching asks first, because it changes what everyone scans, and says what will happen:

- To **own barcodes**: every copy without a barcode is given a generated one, such as `C0000012`, so print labels and stick them on. All copies and loans are kept.
- To **ISBN scanning**: every copy keeps its barcode, and scanning it still finds that copy. All copies and loans are kept.

### Secure connection: the HTTPS fingerprint

When the library uses its own certificate authority (the default on a home or office network), this section shows the authority's **SHA-256 fingerprint**, a string of 32 pairs of hex digits like `A3:39:09:DB:…:36:61`. The server's console prints the same one when it starts.

Use it to be sure a phone or computer installed *your* library's certificate and not someone else's: after following the [trust guide](install-lan.md#the-trust-step-once-per-phone-or-computer), open the certificate's details on that device, and compare its SHA-256 fingerprint with this one. If they differ, remove the certificate. Read this page only from a device that already trusts the library; the trust guide itself is served over plain HTTP, so it is not the place to compare with. The section links to the trust guide on port 8080; if you changed `JL_HTTP_PORT`, use that port.

If the library is served with your own certificate, or behind a reverse proxy that does the HTTPS, there is no authority of its own and no fingerprint: the section says so.

## Statistics and the circulation report

**Statistics** in the side bar (`/admin/statistics`), for anyone with the `reports.view` permission. Everything on it is a count; no patron is named.

**Library statistics** shows, as of now:

| Figure | Counts |
|---|---|
| Books | books in the catalog (removed books aren't counted) |
| Copies | copies of those books |
| Patrons | active accounts that may see their own loans |
| On loan now | loans not yet returned |
| Overdue | loans not yet returned whose due date is before today in the library's time zone (shown highlighted when above zero) |

**Checkouts and returns** counts, for a range of dates you choose, how many books were checked out and how many returned. It starts as the last 30 days. Choose **From** and **To** and **Show report**.

- Both days are included, from the start of the first day to the end of the last, in the library's time zone.
- The range can be at most 5 years. A longer one is refused (`A report can cover at most 5 years. Choose a shorter range.`), as is an end before the start (`The end date can't be before the start date.`).
- Loans of copies or books that were later removed still count.

The same numbers are available to programs as `GET /api/admin/reports/statistics` and `GET /api/admin/reports/circulation?from=YYYY-MM-DD&to=YYYY-MM-DD`, for a signed-in caller with `reports.view`.

## The activity log

**Activity log** in the side bar (`/admin/activity`), for anyone with the `activity.view` permission. It lists what administrators did, newest first, 50 to a page, with the time in the library's time zone, who did it, who it was done to, and what changed. An entry is never edited or deleted, and one for an action that was refused is never made.

What is recorded:

| Entry | When |
|---|---|
| Set up the library | the first administrator was created |
| Changed settings | any setting changed, with each old and new value, such as `Loan period 14 → 21 days` (the logo is shown as set or removed, not the file) |
| Changed a role, Changed a permission | a person's role or one of their permissions changed |
| Deactivated / Reactivated an account | |
| Reset a password | an administrator reset a person's password, or it was reset from the server's console (shown as "Server console") |
| Created / Revoked an invitation | |
| Reissued a card | a patron's library card was replaced |
| Removed a review | a librarian removed a patron's review |

Passwords, invitation codes and setup codes are never written to it. Everyday circulation (checkouts, check-ins, renewals) isn't in this log: each loan records who checked it out and in, and the reports count them.

The log is available to programs as `GET /api/admin/activity` (`page`, `per_page` up to 100).
