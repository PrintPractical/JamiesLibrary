import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, library, meOf, renderApp, signedOut } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const summary = (id: number, title: string, over: Record<string, unknown> = {}) => ({
  id,
  title,
  authors: ["An Author"],
  isbn: "",
  cover_url: "",
  copy_count: 2,
  available: 1,
  ...over,
});

const page = (books: unknown[], over: Record<string, unknown> = {}) => ({ books, total: books.length, page: 1, page_size: 50, ...over });

const locations = [
  { id: 1, name: "Study", copies: 3 },
  { id: 2, name: "Kitchen", copies: 1 },
];

const review = (id: number, name: string, text: string, over: Record<string, unknown> = {}) => ({
  id,
  display_name: name,
  rating: 4,
  text,
  updated_at: "2026-11-02T10:00:00Z",
  ...over,
});

const detail = (over: Record<string, unknown> = {}) => ({
  ...summary(5, "Piranesi", { copy_count: 3, available: 2 }),
  description: "A man lives in the House.",
  copies: [
    { id: 1, number: 1, condition: "good", location: "Study", available: true },
    { id: 2, number: 2, condition: "good", location: "Kitchen", available: true },
    { id: 3, number: 3, condition: "good", location: "Study", available: false, due_date: "2026-11-24" },
  ],
  rating_average: 4.4,
  rating_count: 9,
  my_review: null,
  reviews: [review(11, "Sam L.", "Strange and gentle.")],
  reviews_total: 1,
  ...over,
});

/** A book's row in the results (its cover block repeats the title, so the text alone isn't unique). */
const row = (title: string) => screen.findByRole("link", { name: new RegExp(title) });
const noRow = (title: string) => screen.queryByRole("link", { name: new RegExp(title) });

const member = (role: "patron" | "librarian" = "patron") => ({ "GET /api/me": { body: meOf(role) } });

describe("Browse", () => {
  it("lists books with their availability", async () => {
    fakeServer({
      ...member(),
      "GET /api/books": { body: page([summary(1, "Dune"), summary(2, "Emma", { available: 0 })]) },
      "GET /api/locations": { body: locations },
    });
    await renderApp("/browse");
    expect(await row("Dune")).toBeInTheDocument();
    expect(screen.getByText("1 of 2 copies on the shelf")).toBeInTheDocument();
    expect(screen.getByText(/Unavailable · all 2 copies are out/)).toBeInTheDocument();
  });

  it("CAT-2 No match: says nothing was found", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books?q=zzzz": { body: page([]) },
      "GET /api/locations": { body: locations },
    });
    await renderApp("/browse?q=zzzz");
    expect(await screen.findByRole("heading", { name: "Nothing found" })).toBeInTheDocument();
    expect(screen.getByText(/No book matches “zzzz”/)).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/books?q=zzzz")).toBe(true);
  });

  it("CAT-2 searches by the term typed", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books": { body: page([summary(1, "Dune")]) },
      "GET /api/books?q=hobb": { body: page([summary(2, "The Hobbit")]) },
      "GET /api/locations": { body: locations },
    });
    await renderApp("/browse");
    await row("Dune");
    await userEvent.type(screen.getByLabelText("Search the library"), "hobb{Enter}");
    expect(await row("The Hobbit")).toBeInTheDocument();
    expect(noRow("Dune")).toBeNull();
    expect(calls.map((c) => c.path)).toContain("/api/books?q=hobb");
  });

  it("CAT-8 Filter by location: asks only for the chosen location", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books": { body: page([summary(1, "Dune"), summary(2, "Emma")]) },
      "GET /api/books?location=2": { body: page([summary(2, "Emma")]) },
      "GET /api/locations": { body: locations },
    });
    await renderApp("/browse");
    await row("Dune");
    await userEvent.selectOptions(screen.getByLabelText("Location"), "Kitchen");
    await waitFor(() => expect(noRow("Dune")).toBeNull());
    expect(await row("Emma")).toBeInTheDocument();
    expect(screen.getByText(/in Kitchen/)).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain("/api/books?location=2");
  });

  it("pages through results", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books": { body: page([summary(1, "Dune")], { total: 60 }) },
      "GET /api/books?page=2": { body: page([summary(2, "Emma")], { total: 60, page: 2 }) },
      "GET /api/locations": { body: locations },
    });
    await renderApp("/browse");
    await row("Dune");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await row("Emma")).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain("/api/books?page=2");
  });

  it("ACCESS-4 a visitor searches /api/public/ with no location filter", async () => {
    const calls = fakeServer({
      "GET /api/me": signedOut(),
      "GET /api/library": { body: { ...library, public_catalog: true } },
      "GET /api/public/books": {
        body: page([{ id: 1, title: "Dune", authors: ["Frank Herbert"], isbn: "", description: "", cover_url: "", available_copies: 0, total_copies: 1, rating_average: 0, rating_count: 0 }]),
      },
    });
    await renderApp("/browse");
    expect(await row("Dune")).toBeInTheDocument();
    expect(screen.getByText(/Unavailable/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Location")).toBeNull();
    expect(calls.every((c) => !c.path.startsWith("/api/books") && !c.path.startsWith("/api/locations"))).toBe(true);
  });
});

describe("Book page", () => {
  it("shows copies on the shelf, where they are, and a copy that is out with its due date", async () => {
    fakeServer({ ...member(), "GET /api/books/5": { body: detail() } });
    await renderApp("/books/5");
    expect(await screen.findByRole("heading", { name: "Piranesi" })).toBeInTheDocument();
    expect(screen.getByText("2 of 3 copies on the shelf")).toBeInTheDocument();
    const copies = within(screen.getByRole("list", { name: "Copies" })).getAllByRole("listitem");
    expect(copies).toHaveLength(3);
    expect(within(copies[1]!).getByText("Kitchen")).toBeInTheDocument();
    expect(within(copies[2]!).getByText(/Out · due (24 Nov|Nov 24)/)).toBeInTheDocument();
    expect(screen.getByText("4.4")).toBeInTheDocument();
    expect(screen.getByText("· 9 ratings")).toBeInTheDocument();
  });

  it("CAT-4 All copies out: shows as unavailable", async () => {
    fakeServer({
      ...member(),
      "GET /api/books/5": {
        body: detail({
          copy_count: 1,
          available: 0,
          copies: [{ id: 3, number: 1, condition: "good", location: "Study", available: false, due_date: "2026-11-24" }],
        }),
      },
    });
    await renderApp("/books/5");
    expect(await screen.findByText(/Unavailable · the copy is out/)).toBeInTheDocument();
    expect(screen.queryByText(/on the shelf/)).toBeNull();
  });

  it("CAT-6 Rate: posts the rating and review, then shows what was saved", async () => {
    let saved: unknown;
    let loaded = 0;
    fakeServer({
      ...member(),
      "GET /api/books/5": () => ((loaded += 1), { body: loaded === 1 ? detail() : detail({ my_review: review(12, "Pat patron", "Lovely.", { rating: 4 }) }) }),
      "PUT /api/books/5/review": (body) => ((saved = body), { body: review(12, "Pat patron", "Lovely.") }),
    });
    await renderApp("/books/5");
    await userEvent.click(await screen.findByRole("radio", { name: "4 stars" }));
    await userEvent.type(screen.getByLabelText(/Your review/), "Lovely.");
    expect(screen.getByText("7 / 2000")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Post review" }));
    await waitFor(() => expect(saved).toEqual({ rating: 4, text: "Lovely." }));
    expect(await screen.findByRole("button", { name: "Delete my review" })).toBeInTheDocument();
    expect(screen.getByLabelText(/Your review/)).toHaveValue("Lovely.");
  });

  it("CAT-6 Too long: says the limit and does not send it", async () => {
    const calls = fakeServer({ ...member(), "GET /api/books/5": { body: detail() } });
    await renderApp("/books/5");
    const box = await screen.findByLabelText(/Your review/);
    await userEvent.click(box);
    await userEvent.paste("x".repeat(2001));
    expect(screen.getByText(/Too long: a review can be at most 2000 characters, and this one is 2001/)).toBeInTheDocument();
    expect(screen.getByText(/2001 \/ 2000/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Post review" })).toBeDisabled();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("CAT-6 counts characters, not UTF-16 units", async () => {
    fakeServer({ ...member(), "GET /api/books/5": { body: detail() } });
    await renderApp("/books/5");
    await userEvent.click(await screen.findByLabelText(/Your review/));
    await userEvent.paste("😀".repeat(2000));
    expect(screen.getByText("2000 / 2000")).toBeInTheDocument();
    expect(screen.queryByText(/Too long/)).toBeNull();
    expect(screen.getByRole("button", { name: "Post review" })).toBeEnabled();
  });

  it("shows the server's refusal where the review was posted and keeps what was typed", async () => {
    fakeServer({
      ...member(),
      "GET /api/books/5": { body: detail() },
      "PUT /api/books/5/review": apiError(422, "invalid", "A review can be at most 2000 characters; this one is 2001.", "text"),
    });
    await renderApp("/books/5");
    await userEvent.type(await screen.findByLabelText(/Your review/), "Hello");
    await userEvent.click(screen.getByRole("button", { name: "Post review" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("at most 2000 characters");
    expect(screen.getByLabelText(/Your review/)).toHaveValue("Hello");
  });

  it("CAT-6 edit and delete the caller's own review through /api/books/{id}/review", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books/5": { body: detail({ my_review: review(12, "Pat patron", "Lovely.", { rating: 4 }), reviews: [review(12, "Pat patron", "Lovely."), review(11, "Sam L.", "Strange.")], reviews_total: 2 }) },
      "PUT /api/books/5/review": { body: review(12, "Pat patron", "Lovelier.") },
      "DELETE /api/books/5/review": { status: 204 },
    });
    await renderApp("/books/5");
    const box = await screen.findByLabelText(/Your review/);
    expect(box).toHaveValue("Lovely.");
    expect(document.querySelectorAll(".review")).toHaveLength(1); // Sam's: the caller's own is in the form, not repeated

    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    await userEvent.clear(box);
    await userEvent.type(box, "Lovelier.");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ rating: 4, text: "Lovelier." }));
    await userEvent.click(screen.getByRole("button", { name: "Delete my review" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false); // asks first
    await userEvent.click(screen.getByRole("button", { name: "Yes, delete it" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/books/5/review")).toBe(true));
    await waitFor(() => expect(screen.getByLabelText(/Your review/)).toHaveValue(""));
  });

  it("shows reviews with display names to a patron, who has no remove button", async () => {
    fakeServer({ ...member(), "GET /api/books/5": { body: detail() } });
    await renderApp("/books/5");
    expect(await screen.findByText("Sam L.")).toBeInTheDocument();
    expect(screen.getByText("Strange and gentle.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Remove/ })).toBeNull();
  });

  it("CAT-6 Moderate: a librarian removes a review", async () => {
    let removed = false;
    const calls = fakeServer({
      ...member("librarian"),
      "GET /api/books/5": () => ({ body: removed ? detail({ reviews: [], reviews_total: 0 }) : detail() }),
      "DELETE /api/reviews/11": () => ((removed = true), { status: 204 }),
    });
    await renderApp("/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Remove review by Sam L." }));
    await waitFor(() => expect(screen.queryByText("Sam L.")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/reviews/11")).toBe(true);
  });

  it("renders review text and names as text, each isolated in its own direction", async () => {
    const nasty = '<img src=x onerror="alert(1)"> **bold** ‮evil';
    fakeServer({
      ...member(),
      "GET /api/books/5": { body: detail({ reviews: [review(11, "<b>Mallory</b>‮", nasty)], reviews_total: 1 }) },
    });
    const { container } = await renderApp("/books/5");
    const text = await screen.findByText(nasty);
    expect(text).toHaveAttribute("dir", "auto");
    expect(container.querySelector("img[src='x']")).toBeNull();
    expect(container.querySelector(".review b bdi")?.textContent).toBe("<b>Mallory</b>‮");
  });

  it("offers more reviews when there are more than the first page", async () => {
    const calls = fakeServer({
      ...member(),
      "GET /api/books/5": { body: detail({ reviews_total: 2 }) },
      "GET /api/books/5/reviews?page=2": { body: { reviews: [review(13, "Jo", "Second page.")], page: 2, per_page: 20, total: 2 } },
    });
    await renderApp("/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Show more reviews" }));
    expect(await screen.findByText("Second page.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more reviews" })).toBeNull();
    expect(calls.map((c) => c.path)).toContain("/api/books/5/reviews?page=2");
  });

  it("CAT-6 Visitor: sees availability and the average, and no review text, names, copies or locations", async () => {
    const calls = fakeServer({
      "GET /api/me": signedOut(),
      "GET /api/library": { body: { ...library, public_catalog: true } },
      "GET /api/public/books/5": {
        body: { id: 5, title: "Piranesi", authors: ["Susanna Clarke"], isbn: "9781635575637", description: "A man.", cover_url: "", available_copies: 2, total_copies: 3, rating_average: 4.4, rating_count: 9 },
      },
    });
    await renderApp("/books/5");
    expect(await screen.findByRole("heading", { name: "Piranesi" })).toBeInTheDocument();
    expect(screen.getByText("2 of 3 copies on the shelf")).toBeInTheDocument();
    expect(screen.getByText("4.4")).toBeInTheDocument();
    expect(screen.queryByText("Sam L.")).toBeNull();
    expect(screen.queryByRole("list", { name: "Copies" })).toBeNull();
    expect(screen.queryByLabelText(/Your review/)).toBeNull();
    expect(screen.queryByRole("button", { name: /Remove|Post review/ })).toBeNull();
    expect(calls.every((c) => !c.path.startsWith("/api/books"))).toBe(true);
  });

  it("a book that can't be loaded says so and can be retried", async () => {
    fakeServer({ ...member(), "GET /api/books/5": apiError(404, "not_found", "That book doesn't exist.") });
    await renderApp("/books/5");
    expect(await screen.findByText("That book doesn't exist.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
