import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Loan } from "../../api/loans";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

beforeEach(() => {
  // Monday 16 November 2026: the "Friday" of the scenarios is the 20th.
  vi.useFakeTimers({ toFake: ["Date"], now: new Date("2026-11-16T18:00:00Z") });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const loan = (over: Partial<Loan> = {}): Loan => ({
  id: 1,
  copy_number: 1,
  copy_code: "",
  book_id: 7,
  title: "The Wind in the Willows",
  authors: ["Kenneth Grahame"],
  checked_out_at: "2026-11-06T10:00:00Z",
  due_date: "2026-11-20",
  days_late: 0,
  overdue: false,
  renewals: 0,
  ...over,
});

const patron = (self_checkout = false) =>
  meOf("patron", { settings: { time_zone: "UTC", loan_period_days: 14, max_renewals: 2, self_checkout } });

describe("PATRON-1 Patron dashboard and history", () => {
  test("Dashboard: a loan due Friday is shown with its due date", async () => {
    fakeServer({ "GET /api/me": { body: patron() }, "GET /api/me/loans": { body: { loans: [loan()] } } });
    await renderApp("/");
    expect(await screen.findByRole("heading", { name: "The Wind in the Willows" })).toBeInTheDocument();
    expect(screen.getByText("Due Fri 20 Nov")).toBeInTheDocument();
    expect(screen.getByText("You have 1 book out.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Your library card/ })).toHaveAttribute("href", "/card");
  });

  test("Dashboard: overdue loans come first, labelled with the days and the reason, and have no Renew", async () => {
    fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans": {
        body: {
          loans: [
            loan({ id: 1, title: "On time" }),
            loan({ id: 2, title: "Late one", due_date: "2026-11-13", overdue: true, days_late: 3 }),
            loan({ id: 3, title: "Tomorrow", due_date: "2026-11-17" }),
          ],
        },
      },
    });
    await renderApp("/");
    await screen.findByRole("heading", { name: "Late one" });
    const titles = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(titles).toEqual(["Late one", "Tomorrow", "On time"]);
    const late = screen.getByRole("heading", { name: "Late one" }).closest("article")!;
    expect(within(late).getByText("Overdue · 3 days")).toBeInTheDocument();
    expect(within(late).getByText("Was due Fri 13 Nov")).toBeInTheDocument();
    expect(within(late).getByText(/Overdue books can't be renewed/)).toBeInTheDocument();
    expect(within(late).queryByRole("button")).toBeNull();
    expect(screen.getAllByRole("button", { name: /Renew/ })).toHaveLength(2);
    expect(screen.getByText("Due tomorrow")).toBeInTheDocument();
    expect(screen.getByText("You have 3 books out. One is overdue.")).toBeInTheDocument();
  });

  test("Dashboard: with nothing out it says so", async () => {
    fakeServer({ "GET /api/me": { body: patron() }, "GET /api/me/loans": { body: { loans: [] } } });
    await renderApp("/");
    expect(await screen.findByText("You have no books out.")).toBeInTheDocument();
  });

  test("a loans list that can't be loaded says why and can be retried", async () => {
    let fail = true;
    fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans": () => (fail ? apiError(500, "internal", "Something broke (request 9).") : { body: { loans: [loan()] } }),
    });
    await renderApp("/");
    expect(await screen.findByRole("alert")).toHaveTextContent("Something broke");
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "The Wind in the Willows" })).toBeInTheDocument();
  });

  test("History: loans are paged, a removed book still appears, and a still-out loan says so", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans/history?page=1": {
        body: {
          loans: [
            loan({ id: 3, title: "Still here", returned_at: undefined }),
            loan({ id: 2, title: "Gone from the shelf", authors: null, returned_at: "2026-11-02T09:00:00Z" }),
          ],
          page: 1,
          per_page: 2,
          total: 3,
        },
      },
      "GET /api/me/loans/history?page=2": {
        body: { loans: [loan({ id: 1, title: "Oldest", returned_at: "2026-10-01T09:00:00Z" })], page: 2, per_page: 2, total: 3 },
      },
    });
    await renderApp("/loans");
    expect(await screen.findByRole("heading", { name: "Gone from the shelf" })).toBeInTheDocument();
    expect(screen.getByText("Still out")).toBeInTheDocument();
    expect(screen.getByText("Returned 2 Nov 2026")).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Newer/ })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: /Older/ }));
    expect(await screen.findByRole("heading", { name: "Oldest" })).toBeInTheDocument();
    expect(screen.getByText("Page 2 of 2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Older/ })).toBeDisabled();
    expect(calls.filter((c) => c.path.startsWith("/api/me/loans/history"))).toHaveLength(2);
  });

  test("a patron without the permission to see loans is not shown a list, and nothing is asked for", async () => {
    const me = patron();
    me.permissions = ["catalog.browse"];
    const calls = fakeServer({ "GET /api/me": { body: me } });
    await renderApp("/");
    await screen.findByRole("heading", { name: /^Good (morning|afternoon|evening), Pat$/ });
    expect(screen.queryByText("Your books")).toBeNull();
    expect(calls.some((c) => c.path === "/api/me/loans")).toBe(false);
  });

  test("History: no loans yet", async () => {
    fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans/history?page=1": { body: { loans: [], page: 1, per_page: 25, total: 0 } },
    });
    await renderApp("/loans");
    expect(await screen.findByText("You haven't borrowed anything yet.")).toBeInTheDocument();
  });
});

describe("CIRC-5 Renewals", () => {
  test("Renew: the new due date is shown and the server was asked to renew that loan", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans": { body: { loans: [loan()] } },
      "POST /api/me/loans/1/renew": { body: loan({ due_date: "2026-12-04", renewals: 1 }) },
    });
    await renderApp("/");
    expect(await screen.findByText("Renewed 0 of 2 times")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Renew/ }));
    expect(await screen.findByText("Renewed 1 of 2 times")).toBeInTheDocument();
    expect(screen.getByText("Due Fri 4 Dec")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/me/loans/1/renew")).toBe(true);
  });

  test("Over limit: a loan renewed the most times has no Renew and says why", async () => {
    fakeServer({ "GET /api/me": { body: patron() }, "GET /api/me/loans": { body: { loans: [loan({ renewals: 2 })] } } });
    await renderApp("/");
    expect(await screen.findByText(/renewed the most times allowed \(2 of 2\)/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Renew/ })).toBeNull();
  });

  test("Over limit: when the server refuses, its message is shown on that loan and nothing changes", async () => {
    const message = "The Wind in the Willows has been renewed the most times allowed (2), so it can't be renewed again.";
    fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans": { body: { loans: [loan()] } },
      "POST /api/me/loans/1/renew": apiError(409, "conflict", message),
    });
    await renderApp("/");
    await userEvent.click(await screen.findByRole("button", { name: /Renew/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(screen.getByText("Due Fri 20 Nov")).toBeInTheDocument();
  });

  test("Overdue loan: refused by the server when it went overdue after the page loaded, and the list is read again", async () => {
    let calls = 0;
    fakeServer({
      "GET /api/me": { body: patron() },
      "GET /api/me/loans": () =>
        ++calls === 1
          ? { body: { loans: [loan()] } }
          : { body: { loans: [loan({ overdue: true, days_late: 1, due_date: "2026-11-15" })] } },
      "POST /api/me/loans/1/renew": apiError(409, "conflict", "The Wind in the Willows is overdue (due 2026-11-15), so it can't be renewed."),
    });
    await renderApp("/");
    await userEvent.click(await screen.findByRole("button", { name: /Renew/ }));
    expect(await screen.findByText("Overdue · 1 day")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Renew/ })).toBeNull();
    expect(screen.getByRole("alert")).toHaveTextContent("is overdue");
  });
});

describe("CIRC-7 Self-checkout option", () => {
  test("Off: there is no entry point on the home screen, and the screen itself says it is off", async () => {
    fakeServer({ "GET /api/me": { body: patron(false) }, "GET /api/me/loans": { body: { loans: [] } } });
    await renderApp("/");
    await screen.findByText("You have no books out.");
    expect(screen.queryByRole("link", { name: "Check out a book" })).toBeNull();
    await renderApp("/checkout");
    expect(await screen.findByText("Self-checkout is switched off")).toBeInTheDocument();
    expect(screen.queryByLabelText("Book code")).toBeNull();
  });

  test("On: a patron without the permission is not offered it", async () => {
    const me = patron(true);
    me.permissions = me.permissions.filter((p) => p !== "circ.self_checkout");
    fakeServer({ "GET /api/me": { body: me }, "GET /api/me/loans": { body: { loans: [] } } });
    await renderApp("/");
    await screen.findByText("You have no books out.");
    expect(screen.queryByRole("link", { name: "Check out a book" })).toBeNull();
    await renderApp("/checkout");
    expect(await screen.findByText("You don't have access to this page")).toBeInTheDocument();
  });

  test("On: scanning an available copy checks it out, showing the copy number and due date", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: patron(true) },
      "GET /api/me/loans": { body: { loans: [] } },
      "POST /api/me/checkout": { body: loan({ title: "Piranesi", copy_number: 2, copy_code: "C000042", due_date: "2026-11-30" }) },
    });
    await renderApp("/");
    await userEvent.click(await screen.findByRole("link", { name: "Check out a book" }));
    await userEvent.type(await screen.findByLabelText("Book code"), "c000042{Enter}");
    const done = (await screen.findByText("Checked out: Piranesi")).closest(".alert")!;
    expect(done).toHaveTextContent("Checked out: Piranesi");
    expect(done).toHaveTextContent("Copy 2 (C000042), due Mon 30 Nov");
    expect(calls.find((c) => c.path === "/api/me/checkout")?.body).toEqual({ code: "c000042" });
    expect(screen.getByLabelText("Book code")).toHaveValue("");
  });

  test("On, by ISBN: the copy number is shown without an empty code", async () => {
    fakeServer({
      "GET /api/me": { body: patron(true) },
      "POST /api/me/checkout": { body: loan({ title: "Piranesi", copy_number: 3, copy_code: "", due_date: "2026-11-30" }) },
    });
    await renderApp("/checkout");
    await userEvent.type(await screen.findByLabelText("Book code"), "9780593316993{Enter}");
    expect((await screen.findByText("Checked out: Piranesi")).closest(".alert")).toHaveTextContent(/^Checked out: PiranesiCopy 3, due Mon 30 Nov$/);
  });

  test("No copy available: the refusal is shown in the server's words, with the code scanned", async () => {
    const message = "No copy of Piranesi is available: 2 of 2 out.";
    fakeServer({
      "GET /api/me": { body: patron(true) },
      "POST /api/me/checkout": apiError(409, "conflict", message),
    });
    await renderApp("/checkout");
    await userEvent.type(await screen.findByLabelText("Book code"), "9780593316993{Enter}");
    const refusal = await screen.findByRole("alert");
    expect(refusal).toHaveTextContent("Not checked out");
    expect(refusal).toHaveTextContent(message);
    expect(refusal).toHaveTextContent("9780593316993");
    expect(screen.queryByText(/Checked out:/)).toBeNull();
  });

  test("a failed connection is a refusal too, and scanning can go on", async () => {
    fakeServer({ "GET /api/me": { body: patron(true) } });
    await renderApp("/checkout");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await userEvent.type(await screen.findByLabelText("Book code"), "C1{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent("Can't reach the library");
    expect(screen.getByLabelText("Book code")).toBeEnabled();
  });
});
