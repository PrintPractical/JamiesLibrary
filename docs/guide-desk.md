# Working the desk: checking books out and in, patrons and cards

This guide is for librarians: lending and taking back books, looking up a patron, renewing for them, seeing what is overdue, and replacing a lost card. Patrons have their own guide, [Borrowing books](guide-patron.md).

The desk screens are in the staff menu:

| Screen | Address | You need |
|---|---|---|
| **Check out** | `/desk/checkout` | Use the circulation desk (`circ.desk`) |
| **Check in** | `/desk/checkin` | `circ.desk` |
| **Overdue** | `/desk/overdue` | `circ.desk` |
| **Patrons** | `/desk/patrons` | Look up patrons (`patrons.lookup`) |
| **Re-issue card** (a button on a patron's page) | `/desk/patrons/ID` | Re-issue cards (`cards.reissue`) |

Librarians and administrators have all of these by default; patrons have none. See [Accounts, permissions, invitations and recovery](accounts.md#permissions-and-roles).

- [Scanning, typing and the camera](#scanning-typing-and-the-camera)
- [The borrowing rules](#the-borrowing-rules)
- [Checking out](#checking-out)
- [When a checkout is refused](#when-a-checkout-is-refused)
- [Checking in](#checking-in)
- [When several copies of a book are out](#when-several-copies-of-a-book-are-out)
- [Renewing for a patron](#renewing-for-a-patron)
- [Overdue loans](#overdue-loans)
- [Looking up a patron](#looking-up-a-patron)
- [Re-issuing a lost card](#re-issuing-a-lost-card)

## Scanning, typing and the camera

Every code field on these screens takes a handheld scanner that works like a keyboard, typing, or the phone's camera, exactly as described in [Scanning: camera, hand scanner or typing](guide-catalog.md#scanning-camera-hand-scanner-or-typing). Press Enter (a scanner sends it for you) to submit. After each scan the field empties and stays selected, so you can scan one book after another. The camera needs the page served over HTTPS; where it isn't, type or use a hand scanner.

A code is not case-sensitive: `c0000012` and `C0000012` are the same copy.

## The borrowing rules

An administrator sets these in **Settings** (see [Administrator's guide](guide-admin.md)):

- **Loan period**: a new loan is due this many days after today (14 to begin with). Due dates are calendar dates in the library's time zone, so a loan due on the 30th stays on time until midnight at the library, whatever time the server's clock shows.
- **Items out at once**: the most loans a patron can have (5 to begin with).
- **Renewals**: how many times one loan can be renewed (see [Renewing](#renewing-for-a-patron)).
- **How copies are identified**: by ISBN, or by each copy's own barcode. This decides what you scan to identify a book (see [Looking after the catalog](guide-catalog.md#copies-and-how-they-are-identified)).

## Checking out

Open **Check out**.

1. **Scan or type the patron's library card** (12 digits, on their phone or on paper) and press Enter. The patron's name, card number and "*2 of 5 books out*" appear. A red **N overdue** badge shows if they have overdue loans, and an **Already borrowed** list beside the form (for people who may look up patrons) shows what they have out and when it is due.
2. **Scan each book's code.** Each scan is lent straight away and added to **This checkout**, with the copy number and the due date, for example `Copy 2 · Due 30 Nov`. There is no confirm button for each book.
   - **By ISBN** libraries: scan the book's ISBN barcode (the one printed on the book). The lowest-numbered copy that is in is lent, and its number is shown, so you can tell which copy to hand over. A copy's own code, if it has one, also works.
   - **By own barcode** libraries: scan the sticker on the copy. Scanning a book's ISBN here is refused (see below).
3. When you are done, press **Finish**. The screen says `Checked out 3 books to Ann Reader.` and waits for the next card. **Next patron** (top right) starts over without finishing; nothing already lent is undone.

A scan is lent or refused on its own: one refusal never undoes the books lent before it.

## When a checkout is refused

A refusal appears in a box headed **Card not read** (the card step) or **Not checked out** (the book step), with what to do. Nothing is lent when you see one.

| Message | What it means and what to do |
|---|---|
| `No such patron: no library card has that number. Check the card, or type the number again.` | The number matches no card. It may be mistyped, or the card was re-issued and this is the old one: ask the patron to show their current card. |
| `Nothing matches that code. Check the label, or type it again.` | A book code that is no copy's and no book's ISBN. |
| `That is a book's ISBN, and this library identifies copies by their own barcodes. Scan the copy's own barcode.` | The library is in own-barcode mode; scan the sticker on the copy. |
| `<Title> is already out: copy 2 is on loan to Sam Reader, due 30 Nov. If it's in your hand, it was returned without being checked in: check it in first, then scan it again.` | The copy has an open loan. A **Go to check in** link appears. Check the book in, then scan it again. |
| `No copy of <Title> is available: the only copy is out. Check one in first, or wait for a return.` (or `all 3 copies are out`) | By-ISBN mode, and every copy of the book is on loan. |
| `<Title> has no copies in the library to lend. Add a copy to the catalog first.` | The book is in the catalog but has no copy. See [Looking after the catalog](guide-catalog.md). |
| `Limit reached: Ann Reader already has 5 of 5 items out. Check one in before lending another.` | The patron is at the library's limit. |
| `Ann Reader's account is deactivated, so nothing can be checked out to them. An administrator can reactivate it.` | The patron's account was deactivated. The patron's page and the checkout screen show a **Deactivated** badge. |

A copy that has been removed from the catalog is not found: it gets the `Nothing matches that code` refusal.

If the library can't be reached, the screen shows `Can't reach the library. Check your connection and try again.` and nothing is lent for that scan; scan it again once you are back online.

## Checking in

Open **Check in**. You don't pick a patron: scan each returned book's code, a copy's own barcode or, by ISBN, the book's.

Each scan returns its loan at once, and the large card at the top says what happened:

- **Checked in**: the loan is closed, with who had it and when it was due, and, where the copy has a location, `Put it back: Main shelf`. The loan stays in the patron's history.
- **Checked in · returned 3 days late**: the same, for a loan returned after its due date. Nothing else happens: there are no fines in this version.
- **Already in · wasn't checked out**: you scanned a copy that has no open loan. Nothing changed.
- **None is checked out**: you scanned a book's ISBN and no copy of it is on loan: `None is checked out: no copy of <Title> is on loan.`
- **Not found**: `Nothing matches that code`. Check the label, or type it again.

**This session** lists every scan in order, newest first (the last 200), with a badge **On time**, **N days late**, **Already in**, **None out** or **Not found**. **Clear** empties the list; it is kept on this screen only, so it is gone when you leave the page. Each patron's full history is on their page.

Copies are returned only at the desk: patrons can't check books in themselves.

## When several copies of a book are out

In a by-ISBN library the ISBN is the same on every copy. When you scan it:

- if **one** copy is out, that loan is returned;
- if **several** are out, the screen asks `Which copy of <Title> came back?` and lists each open loan with the patron's name, copy number, due date and any days late. Either **scan the patron's library card** (the field is now labelled **Patron's card**), or press **Return** on the right row. Only that loan is returned. **Cancel** drops the question.

While the question is open, a scan is taken as the patron's card. If it isn't a card, it is treated as the next book instead and the question is dropped. If the patron whose card you scanned doesn't have the book out, you are told `Sam Reader doesn't have <Title> out. Choose a loan from the list.`

## Renewing for a patron

Open the patron's page (see [Looking up a patron](#looking-up-a-patron)). Each loan under **On loan now** shows its due date and `N renewals so far`, with **Renew**. A renewal moves the due date out by the loan period *from the current due date*, not from today: a loan due on Friday with a 14-day period is due 14 days after that Friday. The page says `Renewed <Title>: now due 14 Dec 2026.`

A loan can't be renewed when:

| Message | What it means |
|---|---|
| `<Title> is overdue (due Sun 1 Nov), so it can't be renewed. It has to be returned to a librarian.` | A loan past its due date has to be returned (check it in). |
| `<Title> has been renewed the most times allowed (2), so it can't be renewed again. It has to be returned by Mon 28 Dec.` | The library's renewal limit is reached. |
| `That loan was already returned, so there is nothing to renew.` | Someone checked it in meanwhile. |

The message appears under the loan. Renewing needs the circulation desk permission (`circ.desk`); patrons renew their own books from [their home screen](guide-patron.md#renewing-a-book).

## Overdue loans

A loan is overdue once today's date, in the library's time zone, is after its due date. Nothing has to run for this: the status is worked out whenever it is shown. A loan due today is not yet overdue.

**Overdue** (`/desk/overdue`) lists every overdue loan, the longest overdue first, 50 to a page (**Previous** and **Next** below the list). Each row shows the book, the patron (select the name to open their page), `Overdue · 6 days` and the date it was due. When nothing is overdue the page says `Nothing is overdue. Every loan is within its due date.` A loan leaves the list when it is checked in.

There are no fines, reminders or emails in this version: use the list to ask patrons to bring books back.

## Looking up a patron

Open **Patrons**. In **Find a patron**, type part of a name or username, or scan or type a **complete** card number, and press **Find**. A part of a card number finds nothing. Up to 50 patrons are shown, in name order, each with their username, card number and badges (`2 out`, `1 overdue`, **Deactivated**). A search can have at most 200 characters.

If nobody matches, the page says `No patron matches “…”` and suggests checking the spelling or scanning again. A card that was re-issued no longer works.

Select a patron to open their page (`/desk/patrons/ID`):

- the name, username and card number, with their badges;
- **On loan now**: each loan with its due date, renewals so far and an **Overdue** badge where it applies, with **Renew**;
- **Borrowing history**: every loan they have had, newest first, with `Borrowed 16 Nov 2026` and `Returned 20 Nov 2026` or `Still out`, 50 to a page. Loans of books since removed from the catalog are still listed.

Patrons see only their own loans. Staff with the lookup permission see anyone's.

## Re-issuing a lost card

If a patron loses their phone or thinks someone copied their card, open their page and press **Re-issue card** (you need `cards.reissue`). You are asked `Re-issue <Name>'s card?` and told the old card will stop working straight away. **Yes, re-issue the card** makes a new 12-digit number and shows it once on the page (`New card issued`); the patron can see it under **My card** the next time they sign in or reload.

- The old number is gone: scanning it gives `No such patron: no library card has that number.` Loans the patron already has are unchanged and stay theirs.
- The new number is never one a copy or another card has.
- You can't change the card of someone who holds permissions you don't: `You can't change the card of someone who has permissions you don't have.` An administrator can.
- The action is recorded in the [activity log](guide-admin.md#the-activity-log) with who did it and for whom (never the card numbers).
