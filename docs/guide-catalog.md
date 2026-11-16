# Looking after the catalog: books, copies, locations and labels

This guide is for librarians: anyone with the **Manage the catalog** permission (`catalog.manage`). Librarians and administrators have it by default; an administrator can give it to anyone (see [Accounts and permissions](accounts.md)). It covers adding books by scanning, copies and how they are identified, locations, changing and removing, and printing labels. How patrons find books is in [Finding books](guide-browsing.md).

- [Where things are](#where-things-are)
- [Add a book](#add-a-book)
- [Scanning: camera, hand scanner or typing](#scanning-camera-hand-scanner-or-typing)
- [When the lookup can't be reached](#when-the-lookup-cant-be-reached)
- [Copies and how they are identified](#copies-and-how-they-are-identified)
- [Locations](#locations)
- [Change or remove a book or copy](#change-or-remove-a-book-or-copy)
- [Print labels](#print-labels)
- [Search, book pages and reviews](#search-book-pages-and-reviews)
- [The public catalog](#the-public-catalog)

## Where things are

**Catalog** in the side bar (or the tab bar on a phone) opens `/catalog`. It has three buttons, **Add a book**, **Locations** and **Print labels**, and a search box, **Find a book to change**, that takes a title, author or ISBN and lists the matching books. Select a book to change it. A book's ordinary page (`/books/ID`) also has a **Change this book** button for you.

## Add a book

Open **Catalog**, then **Add a book** (`/catalog/new`).

1. **Scan the book's ISBN barcode, or type the ISBN** and press **Look up**. An ISBN can have 10 or 13 digits, with or without hyphens. It is checked, and stored as 13 digits. A mistyped one is refused: `That is not a valid ISBN. Check the digits (an ISBN has 10 or 13) and try again.`
2. The library asks Open Library for the title, authors, description and cover. One of these happens:
   - **Found on Open Library:** the form is filled in. Check it. Nothing is saved until you press **Add**.
   - **Not found on Open Library:** fill in the details yourself.
   - **Lookup unavailable:** see [below](#when-the-lookup-cant-be-reached).
   - **This book is already in the catalog:** no second book is made. The page names the book and offers to add copies to it.
3. Fill in or correct the **Book details**:
   - **Title** is required (up to 500 characters).
   - **Authors**: at least one, with several separated by commas.
   - **Description** is optional.
   - **Cover**: **Add a cover** or **Replace cover** takes a JPEG, PNG or WebP image up to 5 MB. A cover found by the lookup is kept on your own server, so patrons' devices never load it from elsewhere.
4. Under **Copies**, say how many copies you have (with **+** and **−**, up to 20 at a time) and, for each, its **Location** and **Condition** (new, good, fair, worn, poor or damaged; good to begin with). See [Copies](#copies-and-how-they-are-identified).
5. Press **Add book & 2 copies** (the number follows your count).

For a book with no ISBN, press **Enter without an ISBN** under the ISBN box and fill in the details yourself. Its copies always carry barcodes of their own.

A book can't be saved without a copy's location. If a copy is refused part-way (say, a sticker barcode that is taken), the book and the copies already made are saved and marked **Saved**, the book's details are locked, and you fix the marked copy and press **Save**. Change the details afterwards from the book's page.

When it is saved, the **Saved** screen lists each copy with its barcode, and offers to **Print labels** for them at once (see [Print labels](#print-labels)), **View the book**, or **Add another book**.

## Scanning: camera, hand scanner or typing

Every place the app reads a barcode is the same field, and you can use any of three ways:

- **A handheld scanner** that works like a keyboard: click in the field (on **Add a book** it is already selected), scan, and the scanner's Enter submits it.
- **Typing:** type the code and press Enter, or the button beside the field.
- **The phone's camera:** press the camera button beside the field and hold the barcode in view. A light button turns the phone's torch on where the phone has one, and the cross closes the camera.

The camera works only when the page is served securely (HTTPS). On a home or office network that means doing the one-time trust step on each phone or computer, described in [the trust guide](install-lan.md#the-trust-step-once-per-phone-or-computer). Until a device has, the field says `Camera scanning needs a one-time step on this device.` and links to the guide; the scanner and typing work regardless. Other messages: `This device has no camera the app can use.`, `The camera is blocked for this site. Allow it in the browser's settings.` and `The camera couldn't start.`

## When the lookup can't be reached

The library asks Open Library, on the internet, for a book's details and cover, and that is the only thing it ever asks the internet for. Everything else, including the web app itself, works with no internet connection.

If the library has no internet, or Open Library doesn't answer, the lookup gives up after about 10 seconds and the page says **Lookup unavailable**: `Open Library could not be reached. Enter the details by hand, or look the ISBN up again later.` The form stays open and nothing is lost: type the title, authors and description, add a cover from a file if you have one, and add the book as usual. The ISBN you scanned is kept with it.

If instead the page says `Can't reach the library. Check your connection and try again.`, it is your phone or computer that can't reach the library's server (for example, it has left the Wi-Fi). Nothing was saved by that step. Reconnect and repeat it.

## Copies and how they are identified

A **copy** is one physical item. A book can have many, numbered 1, 2, 3 and so on within the book, each with its own location and condition. A copy that is on loan shows **On loan** and one that is in shows **On the shelf**.

The library identifies copies in one of two ways, chosen by an administrator under **Settings**:

| | **By ISBN** (the default) | **By own barcode** |
|---|---|---|
| What is scanned | The book's ISBN barcode, which is already on the book | A barcode sticker on the copy |
| Copies of one book | Share the ISBN barcode, so none needs a sticker | Each has its own |
| At checkout | The lowest-numbered copy that is in is lent, and the desk is told which | The scanned copy is lent |
| When adding a copy | Nothing to choose | **New label** (a barcode such as `C0000012` is made when the copy is saved) or **Use a sticker** |

**Use a sticker** is for a barcode already stuck on the item: scan or type it. It is stored in capitals, up to 32 characters with letters, digits and punctuation from the keyboard and no spaces. It is refused if another copy has it (`Another copy already has this barcode. Each copy needs its own.`) or if it is a library card number (`That code is a library card number, so it can't be a copy's barcode. Use a different barcode.`).

**A book with no ISBN** always has copies with their own barcodes, in either mode.

**Switching** is done in **Settings** by an administrator, who is asked first. Moving to own barcodes gives every copy that has none a generated barcode, so print labels and stick them on. Moving back to ISBN changes nothing but the setting: copies keep their barcodes and scanning one still finds it. No copy or loan is lost either way. See [How copies are identified](guide-admin.md#how-copies-are-identified).

## Locations

Every copy lives in one named location, such as a room, a shelf or a branch. A new library has one, **Main shelf**. Open **Catalog**, then **Locations** (`/catalog/locations`).

- **Add a location:** type a name (up to 100 characters) and press **Add location**. Names are unique, ignoring capitals: `A location with this name already exists.` You can also make one while adding a copy, by choosing **New location…** in its list.
- **Rename** changes the name for every copy there.
- **Delete** works only on an empty location. If it holds copies the page says how many and offers to **move them** to another location and delete this one in one step. With no other location, add one first.

Browsing can be limited to a location, once the library has more than one.

## Change or remove a book or copy

Find the book on **Catalog** and select it (`/catalog/books/ID`).

- **Book details**, including the ISBN (leave it empty for a book with none) and the cover, are changed in the form; press **Save changes**. Changing the ISBN to one that another book has is refused, with a link to that book.
- **Copies** are listed with their location, condition and whether they are in. **Edit** changes a copy's location and condition. **Add a copy** adds one more. A copy's barcode isn't changed from here.
- **Remove** on a copy asks first: `Remove copy 2? It leaves the catalog and its label stops working.` Its past loans stay in patrons' histories and in reports. Its barcode can be used again.
- **Remove book…** at the foot of the page removes the book and all its copies after a question. They can't be scanned or found again, and past loans stay in histories and reports.

A copy that is on loan can't be removed, and a book with a copy on loan can't be removed: `Copy 2 is on loan and can't be removed: check it in first.` Check it in at the desk first.

## Print labels

Labels are made as a PDF, so the barcodes land exactly on the sheet's labels. Open **Catalog**, then **Print labels** (`/catalog/labels`), or use **Print labels** beside a book's copies when you are changing it, to start with that book:

1. Search for a book (title, author or ISBN), select it, and tick the copies you want, or **All copies**. Search again to add copies of other books. They are listed on the right, where **Remove** leaves one out.
2. Choose the **Label sheet**:
   - **Avery 5160** (US Letter, 3 across and 10 down: 30 labels, each 2⅝ × 1 in);
   - **Avery L7160** (A4, 3 across and 7 down: 21 labels, each 63.5 × 38.1 mm);
   - **One label per page** (62 × 29 mm), for a label printer.
3. Press **Make the labels**. The PDF opens in a new tab; if it doesn't, use **Open the PDF**. Print it at actual size (100%, not "fit to page"), or the labels will drift off the sheet's grid.

Up to 5,000 labels are made at once, filling each sheet row by row from the top left, and further sheets as needed. Each label's contents follow how the library identifies copies:

- **By ISBN:** the book's ISBN barcode (EAN-13), its title, the copy number and the location. A book with no ISBN carries its copy's own barcode in place of the ISBN's.
- **By own barcode:** the copy's barcode (Code 128) and the book's title.

Text too long for a label is cut short with an ellipsis rather than running into the next label. The **Saved** screen after adding a book offers the same step for the copies just added.

## Search, book pages and reviews

Anyone who may browse the catalog finds books on **Browse** and reads a book's page, with its cover, average rating, copies and description. You see a copy's location and due date; patrons are never named. A due date shows the year if it isn't this year (for example `Out · due 24 Nov 2027`). Anyone signed in with the right permission can rate a book 1 to 5 and write a review of up to 2,000 characters, once each.

As a librarian you can also **Remove** a review that shouldn't stay. You are asked to confirm, it is recorded in the activity log, and its writer can't post a review of that book again. See [Finding books](guide-browsing.md) for all of this.

## The public catalog

An administrator can switch on **Public catalog** in **Settings**. Then anyone, with no account, can search the catalog and open a book's page, and sees each book's title, authors, ISBN, description, cover, how many copies are on the shelf and its average rating. They never see where a copy is, due dates, who has a book, or reviews: a visitor is asked to sign in to read those. Switched off, which it is at first, the library is private and a visitor is sent to the sign-in page. See [The public catalog](guide-browsing.md#without-an-account-the-public-catalog).
