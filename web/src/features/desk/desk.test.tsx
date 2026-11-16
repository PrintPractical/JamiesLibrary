import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const overdue = (total: number) => ({ body: { loans: [], page: 1, per_page: 25, total } });

describe("UX-2 Staff Desk home", () => {
  test("a librarian has Check out and Check in as big one-tap actions, and Add a book", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: /^Desk, Pat$/ })).toBeInTheDocument();
    const main = screen.getByRole("main");
    expect(within(main).getByRole("link", { name: "Check out" })).toHaveAttribute("href", "/desk/checkout");
    expect(within(main).getByRole("link", { name: "Check in" })).toHaveAttribute("href", "/desk/checkin");
    expect(within(main).getByRole("link", { name: "Add a book" })).toHaveAttribute("href", "/catalog/new");
  });

  test("today's overdue count links to the overdue list", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(3) });
    await renderApp("/desk");
    const link = await screen.findByRole("link", { name: /3 loans past the due date/ });
    expect(link).toHaveAttribute("href", "/desk/overdue");
  });

  test("nothing overdue says so, with no link", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    expect(await screen.findByText(/Nothing is overdue/)).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /past the due date/ })).toBeNull();
  });

  test("a failed overdue count is shown with a way to retry", async () => {
    let n = 0;
    fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": () => (n++ === 0 ? apiError(500, "internal", "Something went wrong.") : overdue(1)),
    });
    await renderApp("/desk");
    await userEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("link", { name: /1 loan past the due date/ })).toBeInTheDocument();
  });

  test("the lookup box opens the patron search with what was typed", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: meOf("librarian") },
      "GET /api/desk/overdue?page=1": overdue(0),
      "GET /api/patrons?q=maya": { body: { patrons: [{ id: 7, username: "maya", display_name: "Maya Okafor", card: "123456789012", active: true, out: 0, overdue: 0 }] } },
    });
    await renderApp("/desk");
    await userEvent.type(await screen.findByLabelText("Name or card number"), "maya{Enter}");
    expect(await screen.findByRole("link", { name: /Maya Okafor/ })).toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/api/patrons?q=maya")).toHaveLength(1);
  });

  test("an empty lookup asks for a name instead of searching", async () => {
    const calls = fakeServer({ "GET /api/me": { body: meOf("librarian") }, "GET /api/desk/overdue?page=1": overdue(0) });
    await renderApp("/desk");
    await userEvent.click(await screen.findByRole("button", { name: "Find" }));
    expect(screen.getByText(/Type a name or a card number/)).toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith("/api/patrons"))).toBe(false);
  });

  test("permissions: only what the user may do is shown", async () => {
    // Look up patrons only: no check out, check in, overdue or add.
    fakeServer({ "GET /api/me": { body: meOf("patron", { permissions: ["patrons.lookup"] }) } });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByLabelText("Name or card number")).toBeInTheDocument();
    expect(within(main).queryByRole("link", { name: "Check out" })).toBeNull();
    expect(within(main).queryByRole("link", { name: "Check in" })).toBeNull();
    expect(within(main).queryByRole("link", { name: "Add a book" })).toBeNull();
    expect(within(main).queryByText("Overdue today")).toBeNull();
  });

  test("permissions: a catalog manager gets Add a book and no lookup or circulation", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron", { permissions: ["catalog.manage"] }) } });
    await renderApp("/desk");
    const main = await screen.findByRole("main");
    expect(await within(main).findByRole("link", { name: "Add a book" })).toBeInTheDocument();
    expect(within(main).queryByRole("link", { name: "Check out" })).toBeNull();
    expect(within(main).queryByLabelText("Name or card number")).toBeNull();
  });

  test("permissions: an administrator without desk rights is told so, not shown a blank page", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron", { permissions: ["settings.manage"] }) } });
    await renderApp("/desk");
    expect(await screen.findByText(/no desk actions/)).toBeInTheDocument();
  });
});
