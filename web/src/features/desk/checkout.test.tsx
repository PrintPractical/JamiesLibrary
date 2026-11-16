import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp, type Handler, type Reply } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const patron = { id: 7, display_name: "Maya Okafor", card: "L2041187", active: true, out: 5, overdue: 1, limit: 6 };
const loan = (n: number, title: string, code: string) => ({
  id: n,
  copy_id: n,
  copy_number: 1,
  copy_code: code,
  book_id: n,
  title,
  authors: [],
  patron_name: "Maya Okafor",
  due_date: "2026-11-30",
  days_late: 0,
  overdue: false,
});

const base: Record<string, Handler | Reply> = {
  "GET /api/me": { body: meOf("librarian") },
  "POST /api/desk/resolve": (b) =>
    (b as { code: string }).code === "L2041187"
      ? { body: { kind: "patron", patron } }
      : apiError(404, "not_found", "No such patron: no library card has that number. Check the card, or type the number again."),
  "GET /api/patrons/7": {
    body: { patron, current: [{ ...loan(3, "The Secret Garden", "C-000033"), overdue: true, days_late: 3 }], overdue: [] },
  },
};

async function scan(label: string, code: string) {
  await userEvent.type(screen.getByLabelText(label), `${code}{Enter}`);
}

async function beginSession() {
  await renderApp("/desk/checkout");
  await scan("Library card", "L2041187");
  await screen.findByRole("heading", { name: "Maya Okafor" });
}

describe("CIRC-1 Check out", () => {
  test("Checkout: the card shows the patron, and each copy scan goes out at once with its due date", async () => {
    const calls = fakeServer({
      ...base,
      "POST /api/desk/checkout": (b) => ({
        body: (b as { code: string }).code === "C-000412" ? loan(1, "Piranesi", "C-000412") : loan(2, "Braiding Sweetgrass", "C-000207"),
      }),
    });
    await beginSession();
    const who = screen.getByRole("region", { name: "Patron" });
    expect(who).toHaveTextContent("L2041187");
    expect(who).toHaveTextContent("5 of 6 books out");
    expect(who).toHaveTextContent("1 overdue");
    expect(screen.getByRole("complementary")).toHaveTextContent("The Secret Garden");
    expect(screen.getByRole("complementary")).toHaveTextContent("Overdue 3 days");

    await scan("Copy barcode", "C-000412");
    await scan("Copy barcode", "C-000207");
    const list = await screen.findByRole("heading", { name: "This checkout · 2 books" });
    const section = list.closest("section")!;
    expect(within(section).getByText("Piranesi")).toBeInTheDocument();
    expect(within(section).getByText("Braiding Sweetgrass")).toBeInTheDocument();
    expect(section).toHaveTextContent("Due Mon 30 Nov");
    expect(section).toHaveTextContent("Copy 1 · Due 30 Nov");
    expect(who).toHaveTextContent("7 of 6 books out"); // counts what went out too
    // no confirmation step: the card, then one checkout call per scan
    expect(calls.filter((c) => c.path === "/api/desk/checkout").map((c) => c.body)).toEqual([
      { patron: "L2041187", code: "C-000412" },
      { patron: "L2041187", code: "C-000207" },
    ]);
  });

  test("Copy already out: its message is shown with the way out, and the session carries on", async () => {
    let n = 0;
    fakeServer({
      ...base,
      "POST /api/desk/checkout": (b) =>
        (b as { code: string }).code === "C-000318"
          ? apiError(409, "conflict", "The Hobbit is already out: copy 1 is on loan to Sam Lee, due 2026-11-20. Check it in first, then scan it again.")
          : { body: loan(++n, "Piranesi", "C-000412") },
    });
    await beginSession();
    await scan("Copy barcode", "C-000318");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not checked out");
    expect(alert).toHaveTextContent("The Hobbit is already out");
    expect(within(alert).getByRole("link", { name: "Go to check in" })).toHaveAttribute("href", "/desk/checkin");
    expect(screen.getByRole("region", { name: "Patron" })).toHaveTextContent("Maya Okafor");
    await scan("Copy barcode", "C-000412");
    expect(await screen.findByRole("heading", { name: "This checkout · 1 book" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("SCAN-4 Scan library card", () => {
  test("Unknown card: the message is shown, nothing starts, and another card can be tried", async () => {
    fakeServer(base);
    await renderApp("/desk/checkout");
    await scan("Library card", "000000000000");
    expect(await screen.findByRole("alert")).toHaveTextContent("No such patron");
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
    await scan("Library card", "L2041187");
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
  });
});

describe("SCAN-3 Scan copy barcode", () => {
  test("Unknown code: the message is shown and the session carries on", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/checkout": apiError(404, "not_found", "Nothing matches that code. Check the label, or type it again."),
    });
    await beginSession();
    await scan("Copy barcode", "ZZZ");
    expect(await screen.findByRole("alert")).toHaveTextContent("Nothing matches that code.");
    expect(screen.queryByRole("link", { name: "Go to check in" })).toBeNull();
    expect(screen.getByLabelText("Copy barcode")).toBeInTheDocument();
  });

  test("a book's ISBN in own-barcode mode is told to scan the copy's own barcode", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/checkout": apiError(422, "invalid", "That is a book's ISBN, and this library identifies copies by their own barcodes. Scan the copy's own barcode."),
    });
    await beginSession();
    await scan("Copy barcode", "9780140449136");
    expect(await screen.findByRole("alert")).toHaveTextContent("Scan the copy's own barcode.");
  });
});

describe("CIRC-4 Borrowing policy", () => {
  test("Limit reached: the message is shown, the list is unchanged, and the way out is offered", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/checkout": apiError(409, "conflict", "Limit reached: Maya Okafor already has 6 of 6 items out. Check one in before lending another."),
    });
    await beginSession();
    await scan("Copy barcode", "C-000412");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Limit reached: Maya Okafor already has 6 of 6 items out.");
    expect(within(alert).getByRole("link", { name: "Go to check in" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "This checkout · 0 books" })).toBeInTheDocument();
  });
});

describe("the session", () => {
  test("Next patron ends it: the next card starts a fresh list, and Finish says what went out", async () => {
    fakeServer({ ...base, "POST /api/desk/checkout": { body: loan(1, "Piranesi", "C-000412") } });
    await beginSession();
    await scan("Copy barcode", "C-000412");
    await userEvent.click(await screen.findByRole("button", { name: "Finish · 1 book" }));
    expect(await screen.findByText("Checked out 1 book to Maya Okafor.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
    await scan("Library card", "L2041187");
    await waitFor(() => expect(screen.getByRole("heading", { name: "This checkout · 0 books" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Next patron" }));
    expect(screen.getByLabelText("Library card")).toBeInTheDocument();
  });

  test("a dropped connection is said, and the session carries on", async () => {
    fakeServer(base);
    await beginSession();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await scan("Copy barcode", "C-000412");
    expect(await screen.findByRole("alert")).toHaveTextContent("Can't reach the library");
    expect(screen.getByLabelText("Copy barcode")).toBeInTheDocument();
  });

  test("a user without the desk permission isn't shown the screen", async () => {
    fakeServer({ ...base, "GET /api/me": { body: meOf("patron") } });
    await renderApp("/desk/checkout");
    expect(screen.queryByLabelText("Library card")).toBeNull();
  });
});
