import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const overdue = (total: number) => ({ body: { loans: [], page: 1, per_page: 25, total } });
const staff = (permissions: string[]) => meOf("patron", { permissions });
const maya = { id: 7, username: "maya", display_name: "Maya Okafor", card: "123456789012", active: true, out: 0, overdue: 0 };

describe("UX-2 Staff Desk home", () => {
  test("a librarian has Check out and Check in as large links, Check in in the secondary style, and Add a book", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: "Desk" })).toBeInTheDocument();
    const main = screen.getByRole("main");
    const out = within(main).getByRole("link", { name: /Check out/ });
    const inn = within(main).getByRole("link", { name: /Check in/ });
    expect(out).toHaveAttribute("href", "/desk/checkout");
    expect(inn).toHaveAttribute("href", "/desk/checkin");
    expect(out).not.toHaveClass("desk-big-alt");
    expect(inn).toHaveClass("desk-big-alt");
    expect(within(main).getByRole("link", { name: /Add a book/ })).toHaveAttribute("href", "/catalog/new");
  });

  test("N loans overdue links to the overdue list", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(3) });
    await renderApp("/desk");
    const link = await screen.findByRole("link", { name: /3\s*loans overdue/ });
    expect(link).toHaveAttribute("href", "/desk/overdue");
    expect(link.querySelector(".desk-od-zero")).toBeNull();
  });

  test("one is '1 loan overdue'", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(1) });
    await renderApp("/desk");
    expect(await screen.findByRole("link", { name: /1\s*loan overdue/ })).toBeInTheDocument();
  });

  test("none overdue shows 0 in the ok colour with 'Nothing overdue', and still links to the list", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    const link = await screen.findByRole("link", { name: /Nothing overdue/ });
    expect(link).toHaveAttribute("href", "/desk/overdue");
    expect(link.querySelector(".desk-od-zero")).toHaveTextContent("0");
  });

  test("the count says nothing for 300ms, then 'Loading…', in the card's frame", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    // The count never answers.
    const answer = fetch;
    vi.stubGlobal("fetch", (path: string, init?: RequestInit) =>
      path.startsWith("/api/desk/overdue") ? new Promise<Response>(() => {}) : answer(path, init),
    );
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: "Desk" })).toBeInTheDocument();
    expect(screen.queryByText("Loading…")).toBeNull();
    await act(async () => {
      vi.advanceTimersByTime(400);
    });
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(within(screen.getByRole("main")).getByRole("link", { name: /Check out/ })).toBeInTheDocument();
  });

  test("a failed count says so with a quiet Retry, and the rest of the desk works", async () => {
    let n = 0;
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": () => (n++ === 0 ? apiError(500, "internal", "Something went wrong.") : overdue(1)),
    });
    await renderApp("/desk");
    expect(await screen.findByText("Couldn't count overdue loans.")).toBeInTheDocument();
    expect(within(screen.getByRole("main")).getByRole("link", { name: /Check in/ })).toBeInTheDocument();
    const retry = screen.getByRole("button", { name: "Retry" });
    expect(retry).toHaveClass("btn-quiet");
    await userEvent.click(retry);
    expect(await screen.findByRole("link", { name: /1\s*loan overdue/ })).toBeInTheDocument();
  });

  test("the search button is named Search, and typing a name then Enter opens the patron search", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": overdue(0),
      "GET /api/patrons?q=maya": { body: { patrons: [maya] } },
    });
    await renderApp("/desk");
    expect(await screen.findByRole("button", { name: "Search" })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Find a patron"), "maya{Enter}");
    expect(await screen.findByRole("link", { name: /Maya Okafor/ })).toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/api/patrons?q=maya")).toHaveLength(1);
  });

  test("a card number typed fast and ended with Enter (a handheld scanner) searches like typing", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": overdue(0),
      "GET /api/patrons?q=123456789012": { body: { patrons: [maya] } },
    });
    await renderApp("/desk");
    await userEvent.type(await screen.findByLabelText("Find a patron"), "123456789012{Enter}", { delay: null });
    expect(await screen.findByRole("link", { name: /Maya Okafor/ })).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/patrons?q=123456789012")).toBe(true);
  });

  test("an empty search stays on the desk with the field focused, and shows no error", async () => {
    const calls = fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    await userEvent.click(await screen.findByRole("button", { name: "Search" }));
    expect(screen.getByLabelText("Find a patron")).toHaveFocus();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("heading", { name: "Desk" })).toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith("/api/patrons"))).toBe(false);
  });

  test("only patron lookup: no check out, check in, overdue or add, and nothing disabled in their place", async () => {
    fakeServer({ "GET /api/me": { body: staff(["patrons.lookup"]) } });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByLabelText("Find a patron")).toBeInTheDocument();
    expect(within(main).queryByRole("link", { name: /Check out/ })).toBeNull();
    expect(within(main).queryByRole("link", { name: /Check in/ })).toBeNull();
    expect(within(main).queryByRole("link", { name: /Add a book/ })).toBeNull();
    expect(within(main).queryByRole("link", { name: /overdue/i })).toBeNull();
    expect(within(main).queryByText(/nothing for you/)).toBeNull();
  });

  test("only circulation: the big actions and the count, but no lookup or add", async () => {
    fakeServer({ "GET /api/me": { body: staff(["circ.desk"]) }, "GET /api/desk/overdue?page=1": overdue(2) });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByRole("link", { name: /2\s*loans overdue/ })).toBeInTheDocument();
    expect(within(main).getByRole("link", { name: /Check out/ })).toBeInTheDocument();
    expect(within(main).queryByLabelText("Find a patron")).toBeNull();
    expect(within(main).queryByRole("link", { name: /Add a book/ })).toBeNull();
  });

  test("only catalog management: Add a book, with no big actions or cards that aren't theirs", async () => {
    fakeServer({ "GET /api/me": { body: staff(["catalog.manage"]) } });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByRole("link", { name: /Add a book/ })).toBeInTheDocument();
    expect(within(main).queryByRole("link", { name: /Check out/ })).toBeNull();
    expect(within(main).queryByLabelText("Find a patron")).toBeNull();
  });

  test("none of the five: the info card, and links to the admin pages they do have", async () => {
    fakeServer({ "GET /api/me": { body: staff(["settings.manage", "reports.view"]) } });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByText("There's nothing for you at the desk.")).toBeInTheDocument();
    expect(within(main).getByText("Your administrator decides what you can do here.")).toBeInTheDocument();
    const admin = within(main).getByRole("navigation", { name: "Admin pages" });
    expect(within(admin).getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/admin/settings");
    expect(within(admin).getByRole("link", { name: "Statistics" })).toHaveAttribute("href", "/admin/statistics");
    expect(within(admin).queryByRole("link", { name: "Activity log" })).toBeNull();
  });

  test("none of the five and no admin pages: only the info card", async () => {
    fakeServer({ "GET /api/me": { body: staff(["reviews.remove"]) } });
    await renderApp("/desk");
    expect(await screen.findByText("There's nothing for you at the desk.")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Admin pages" })).toBeNull();
  });
});
