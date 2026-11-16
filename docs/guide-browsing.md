# Finding books: browsing, search and book pages

This guide is for anyone who looks through the library's catalog: patrons, librarians, and, if the administrator has turned the public catalog on, visitors without an account. Librarians add books on the Add a book screen (`/catalog/new`); there is no guide for it yet.

You need the **Browse** permission (`catalog.browse`) to browse while signed in. Patrons, librarians and administrators have it by default.

- [Browse and search](#browse-and-search)
- [A book's page](#a-books-page)
- [Ratings and reviews](#ratings-and-reviews)
- [Without an account: the public catalog](#without-an-account-the-public-catalog)

## Browse and search

Open **Browse** (`/browse`). With nothing typed in, it lists every book, a page at a time, with **Previous** and **Next** under the list when there is more than one page. Each row shows the cover, the title, the authors and whether the book can be borrowed now.

Type words into **Search the library** and press **Search**. Words are looked for in titles and authors, so `austen` and `pride` both work. A whole ISBN, with or without hyphens, finds that book exactly. A search can be up to 200 characters. The address bar holds the search, so you can bookmark a search and the browser's back button returns to it.

If the library has more than one named location (a shelf, a room, a branch), a **Location** list appears beside the search. Choose one to see only the books that have a copy there; **All locations** removes the filter.

If nothing matches, the page says so and suggests checking the spelling or using fewer words. A new library with no books says "There are no books in the library yet."

Each row says one of:

| Shown | Meaning |
|---|---|
| `2 of 3 copies on the shelf` | Two of the three copies can be borrowed now. |
| `Unavailable · all 3 copies are out` | Every copy is on loan. (`the copy is out` for a book with one copy.) |
| `No copies yet` | The book is in the catalog but has no copy to borrow. |

## A book's page

Select a row to open the book's page (`/books/ID`). It shows:

- the cover (a coloured block with the title when there's none), title and authors;
- the average rating, or **No ratings yet**;
- how many copies are on the shelf, and, signed in, each copy with its location and either **On the shelf** or **Out · due 24 Nov**;
- **About this book**: the description and the ISBN;
- **Ratings & reviews** (see below).

A copy's row shows when it is due back, never who has it.

## Ratings and reviews

Anyone signed in who may read the catalog sees a book's average rating, how many ratings it rests on, and its reviews, each with its writer's display name and date, most recently written or changed first. **Show more reviews** loads the next page.

**Writing one.** With the permission to write reviews (`reviews.write`; patrons have it by default) the page has a **Your rating** form: choose 1 to 5 stars, and optionally write a review of up to 2,000 characters. The counter under the box turns into an error when the review is too long: `Too long: a review can be at most 2000 characters`. **Post review** saves it. You can give a rating alone. Each person has one rating and review for each book: saving again (**Save changes**) replaces it, and **Delete my review** (with a question to confirm) removes it so you can start over. Your display name is shown with what you write.

**Removing someone's review.** A person with the permission to remove reviews (`reviews.remove`; librarians and administrators) sees **Remove** on each review, and a confirmation question. The removal is recorded in the [activity log](guide-admin.md#the-activity-log). The writer can't post a review of that book again: they are told `A librarian removed your review of this book, so it can't be posted again.`

## Without an account: the public catalog

If an administrator turns on **Public catalog** in Settings, anyone can open `/browse` and a book's page without signing in. A visitor sees the title, authors, ISBN, description, cover, how many copies are on the shelf and the average rating. They don't see the location of copies, due dates, patrons, or any review: where the reviews would be, the page says **Sign in to read what others thought of this book**. A visitor can search by words only; the location list is for signed-in people.

When the public catalog is off, which is the default, a visitor who opens either address is sent to the sign-in page. See [What patrons can do](guide-admin.md#what-patrons-can-do-self-checkout-and-the-public-catalog).
