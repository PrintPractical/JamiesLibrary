import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Loan, Patron } from "../../api/patrons";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const maya: Patron = { id: 7, username: "maya", display_name: "Maya Okafor", card: "123456789012", active: true, out: 2, overdue: 1 };

const loan = (id: number, title: string, over: Partial<Loan> = {}): Loan => ({
  id,
  copy_code: `C${id}`,
  book_id: id,
  title,
  authors: ["An Author"],
  patron_id: 7,
  patron_name: "Maya Okafor",
  checked_out_at: "2026-11-01T10:00:00Z",
  due_date: "2026-11-30",
  days_late: 0,
  overdue: false,
  renewals: 0,
  ...over,
});

const dune = loan(1, "Dune");
const emma = loan(2, "Emma", { due_date: "2026-11-10", days_late: 5, overdue: true });
const history = { loans: [dune, emma, loan(3, "Persuasion", { returned_at: "2026-10-20T09:00:00Z" })], page: 1, per_page: 25, total: 3 };

function patronServer(extra: Parameters<typeof fakeServer>[0] = {}, role: "librarian" | "patron" = "librarian") {
  return fakeServer({
    "GET /api/me": { body: meOf(role) },
    "GET /api/patrons/7": { body: { patron: maya, current: [dune, emma], overdue: [emma] } },
    "GET /api/patron-loans/7?page=1": { body: history },
    ...extra,
  });
}

describe("CIRC-3 Patron lookup", () => {
  test("Lookup: a librarian finds a patron by name and both loans, the overdue one flagged, are shown", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/patrons?q=maya": { body: { patrons: [maya] } },
      "GET /api/patrons/7": { body: { patron: maya, current: [dune, emma], overdue: [emma] } },
      "GET /api/patron-loans/7?page=1": { body: history },
    });
    await renderApp("/desk/patrons");
    await userEvent.type(screen.getByLabelText("Find a patron"), "maya{Enter}");
    const hit = await screen.findByRole("link", { name: /Maya Okafor/ });
    expect(hit).toHaveTextContent("1 overdue");
    expect(calls.filter((c) => c.path.startsWith("/api/patrons?"))).toHaveLength(1);
    await userEvent.click(hit);

    const now = await screen.findByRole("region", { name: "On loan now" });
    expect(within(now).getByText("Dune")).toBeInTheDocument();
    expect(within(now).getByText("Emma")).toBeInTheDocument();
    expect(within(now).getByText("Overdue · 5 days")).toBeInTheDocument();
    const hist = await screen.findByRole("region", { name: "Borrowing history" });
    expect(within(hist).getByText("Persuasion")).toBeInTheDocument();
    expect(within(hist).getByText(/Returned/)).toBeInTheDocument();
  });

  test("a card is looked up in the same field", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/patrons?q=123456789012": { body: { patrons: [maya] } },
    });
    await renderApp("/desk/patrons");
    await userEvent.type(screen.getByLabelText("Find a patron"), "123456789012{Enter}");
    expect(await screen.findByRole("link", { name: /Maya Okafor/ })).toHaveTextContent("123456789012");
    expect(calls.some((c) => c.path === "/api/patrons?q=123456789012")).toBe(true);
  });

  test("no match says so and what to try; a failure is shown where the search was", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/patrons?q=zed": { body: { patrons: [] } },
      "GET /api/patrons?q=boom": apiError(500, "internal", "Something went wrong. (request abc)"),
    });
    await renderApp("/desk/patrons");
    await userEvent.type(screen.getByLabelText("Find a patron"), "zed{Enter}");
    expect(await screen.findByRole("heading", { name: "No patron matches “zed”" })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Find a patron"), "boom{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
  });

  test("a user without the look-up permission is told so", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron", { permissions: ["circ.desk"] }) } });
    await renderApp("/desk/patrons");
    expect(screen.getByText("You don't have access to this page")).toBeInTheDocument();
  });
});

describe("CIRC-5 Renewals", () => {
  test("Librarian renews: the loan is renewed and the page shows the new due date", async () => {
    let renewed = false;
    const calls = patronServer({
      "POST /api/desk/loans/1/renew": () => {
        renewed = true;
        return { body: { ...dune, due_date: "2026-12-14", renewals: 1 } };
      },
      "GET /api/patrons/7": () => ({
        body: { patron: maya, current: [renewed ? { ...dune, due_date: "2026-12-14", renewals: 1 } : dune, emma], overdue: [emma] },
      }),
    });
    await renderApp("/desk/patrons/7");
    await userEvent.click(await screen.findByRole("button", { name: "Renew Dune" }));
    expect(await screen.findByText(/Renewed Dune: now due/)).toHaveTextContent("14");
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/desk/loans/1/renew")).toBe(true);
    expect(await screen.findByText(/Due .*14.* · 1 renewal so far/)).toBeInTheDocument();
  });

  test("Overdue loan: the refusal says it is overdue, on that loan, and nothing else changes", async () => {
    patronServer({
      "POST /api/desk/loans/2/renew": apiError(409, "conflict", "Emma is overdue (due 2026-11-10), so it can't be renewed. It has to be returned to a librarian."),
    });
    await renderApp("/desk/patrons/7");
    await userEvent.click(await screen.findByRole("button", { name: "Renew Emma" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("is overdue");
    expect(screen.getByRole("button", { name: "Renew Emma" })).toBeEnabled();
    expect(screen.queryByText(/Renewed/)).toBeNull();
  });

  test("Over limit: the refusal is shown", async () => {
    patronServer({
      "POST /api/desk/loans/1/renew": apiError(409, "conflict", "Dune has been renewed the most times allowed (2), so it can't be renewed again."),
    });
    await renderApp("/desk/patrons/7");
    await userEvent.click(await screen.findByRole("button", { name: "Renew Dune" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("most times allowed");
  });

  test("without the desk permission there is no Renew button", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("patron", { permissions: ["patrons.lookup"] }) },
      "GET /api/patrons/7": { body: { patron: maya, current: [dune], overdue: [] } },
      "GET /api/patron-loans/7?page=1": { body: history },
    });
    await renderApp("/desk/patrons/7");
    await screen.findByRole("region", { name: "On loan now" });
    expect(screen.queryByRole("button", { name: /Renew/ })).toBeNull();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });
});

describe("CIRC-6 Overdue", () => {
  test("Overdue list: each loan shows its patron, linking to them, and the days overdue", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": { body: { loans: [emma], page: 1, per_page: 25, total: 1 } },
      "GET /api/patrons/7": { body: { patron: maya, current: [emma], overdue: [emma] } },
      "GET /api/patron-loans/7?page=1": { body: history },
    });
    await renderApp("/desk/overdue");
    expect(await screen.findByText("Emma")).toBeInTheDocument();
    expect(screen.getByText("Overdue · 5 days")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("link", { name: "Maya Okafor" }));
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
  });

  test("nothing overdue says so; a second page is reached with Next", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": { body: { loans: [emma], page: 1, per_page: 1, total: 2 } },
      "GET /api/desk/overdue?page=2": { body: { loans: [], page: 2, per_page: 1, total: 2 } },
    });
    await renderApp("/desk/overdue");
    await screen.findByText("Emma");
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText(/Nothing is overdue/)).toBeInTheDocument();
  });

  test("a failed load offers a retry", async () => {
    let fail = true;
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": () =>
        fail ? apiError(500, "internal", "Something went wrong.") : { body: { loans: [emma], page: 1, per_page: 25, total: 1 } },
    });
    await renderApp("/desk/overdue");
    await screen.findByRole("heading", { name: "Couldn't load this page" });
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Emma")).toBeInTheDocument();
  });
});

describe("CARD-1 Digital library card", () => {
  test("Re-issue: asks first, saying the old card will stop working, then shows the new number", async () => {
    const calls = patronServer({ "POST /api/users/7/card/reissue": { body: { number: "999888777666" } } });
    await renderApp("/desk/patrons/7");
    await userEvent.click(await screen.findByRole("button", { name: "Re-issue card" }));
    const confirm = screen.getByRole("group", { name: "Confirm re-issuing the card" });
    expect(confirm).toHaveTextContent("The old card will stop working");
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await userEvent.click(within(confirm).getByRole("button", { name: "Yes, re-issue the card" }));
    const done = await screen.findByRole("status");
    expect(done).toHaveTextContent("999888777666");
    expect(done).toHaveTextContent("The old card no longer works");
    expect(screen.queryByText("123456789012")).toBeNull();
  });

  test("cancelling changes nothing; a refusal is shown and the old number stays", async () => {
    const calls = patronServer({ "POST /api/users/7/card/reissue": apiError(403, "forbidden", "You can't change this user's card.") });
    await renderApp("/desk/patrons/7");
    await userEvent.click(await screen.findByRole("button", { name: "Re-issue card" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("group")).toBeNull();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Re-issue card" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, re-issue the card" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("You can't change this user's card."));
    expect(screen.getByText("123456789012")).toBeInTheDocument();
  });

  test("without the re-issue permission there is no button", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("patron", { permissions: ["patrons.lookup", "circ.desk"] }) },
      "GET /api/patrons/7": { body: { patron: maya, current: [], overdue: [] } },
      "GET /api/patron-loans/7?page=1": { body: { loans: [], page: 1, per_page: 25, total: 0 } },
    });
    await renderApp("/desk/patrons/7");
    await screen.findByRole("heading", { name: "Maya Okafor" });
    expect(screen.queryByRole("button", { name: "Re-issue card" })).toBeNull();
  });
});
