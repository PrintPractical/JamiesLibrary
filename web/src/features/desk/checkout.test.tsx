import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp, type Handler, type Reply } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const patron = { id: 7, display_name: "Maya Okafor", card: "L2041187", active: true, out: 4, overdue: 1, limit: 6 };
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
  "POST /api/desk/find": (b) =>
    (b as { q: string }).q === "L2041187"
      ? { body: { kind: "patron", patron } }
      : { body: { kind: "matches", patrons: [], more: false } },
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
  await scan("Patron", "L2041187");
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
    expect(who).toHaveTextContent("4 of 6 books out");
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
    expect(who).toHaveTextContent("6 of 6 books out"); // counts what went out too
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

describe("refusals in the server's own words", () => {
  test("a deactivated patron: the badge, the refusal, and the session carries on", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/find": { body: { kind: "patron", patron: { ...patron, active: false } } },
      "POST /api/desk/checkout": apiError(409, "conflict", "Maya Okafor's account is deactivated, so nothing can be checked out to them. An administrator can reactivate it."),
    });
    await beginSession();
    expect(screen.getByRole("region", { name: "Patron" })).toHaveTextContent("Deactivated");
    await scan("Copy barcode", "C-000412");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("account is deactivated");
    expect(within(alert).queryByRole("link", { name: "Go to check in" })).toBeNull();
    expect(screen.getByLabelText("Copy barcode")).toBeInTheDocument();
  });

  test("no copy available", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/checkout": apiError(409, "conflict", "No copy of Piranesi is available: the only copy is out. Check one in first, or wait for a return."),
    });
    await beginSession();
    await scan("Copy barcode", "9780140449136");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("No copy of Piranesi is available");
    expect(within(alert).getByRole("link", { name: "Go to check in" })).toBeInTheDocument();
  });

  test("a copy already out, in the server's wording, still offers check in", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/checkout": apiError(409, "conflict", "The Hobbit is already out: copy 1 is on loan to Sam Lee, due 2026-11-20. If it's in your hand, it was returned without being checked in: check it in first, then scan it again."),
    });
    await beginSession();
    await scan("Copy barcode", "C-000318");
    expect(within(await screen.findByRole("alert")).getByRole("link", { name: "Go to check in" })).toBeInTheDocument();
  });
});

describe("one desk, many patrons", () => {
  test("Next patron frees the desk even when the last request never settled", async () => {
    fakeServer(base);
    await beginSession();
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    await scan("Copy barcode", "C-000412");
    await userEvent.click(screen.getByRole("button", { name: "Next patron" }));
    fakeServer(base);
    await scan("Patron", "L2041187");
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
    expect(screen.queryByText("Searching…")).toBeNull();
  });

  test("two cards scanned before the first answers: the first patron stays, with nothing of the second", async () => {
    const other = { ...patron, id: 8, display_name: "Sam Lee", card: "L9999999" };
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    fakeServer(base);
    const real = globalThis.fetch;
    let first = true;
    vi.stubGlobal("fetch", (path: string, init?: RequestInit) => {
      if (path === "/api/desk/find" && first) {
        first = false;
        return gate.then(() => real(path, init));
      }
      if (path === "/api/desk/find") {
        return Promise.resolve(new Response(JSON.stringify({ kind: "patron", patron: other }), { status: 200 }));
      }
      return real(path, init);
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "L2041187");
    await scan("Patron", "L9999999");
    release();
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Sam Lee" })).toBeNull());
  });
});

describe("SCAN-4 Scan library card", () => {
  test("Unknown card: the message is shown, nothing starts, and another card can be tried", async () => {
    fakeServer(base);
    await renderApp("/desk/checkout");
    await scan("Patron", "000000000000");
    expect(await screen.findByText("No patron matches ‘000000000000’.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
    await scan("Patron", "L2041187");
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

const ann = { id: 11, display_name: "Ann Lee", username: "ann", card: "L1000011", active: true, out: 2, overdue: 0 };
const matches = (patrons: unknown[], more = false) => ({ body: { kind: "matches", patrons, more } });
const findCalls = (calls: { path: string; body: unknown }[]) => calls.filter((c) => c.path === "/api/desk/find").map((c) => c.body);
const resolveCalls = (calls: { path: string; body: unknown }[]) => calls.filter((c) => c.path === "/api/desk/resolve").map((c) => c.body);

describe("CIRC-8 Find a patron by name at checkout", () => {
  test("a name lists the matches with name, username, card and books out; choosing one reads its card", async () => {
    const calls = fakeServer({
      ...base,
      "POST /api/desk/find": matches([ann, { ...ann, id: 12, display_name: "Ann Marie Ko", username: "annko", card: "L1000012", out: 0 }]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    const list = (await screen.findByRole("heading", { name: "Matches for ‘ann’" })).closest("section")!;
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(list).toHaveTextContent("Ann Lee");
    expect(list).toHaveTextContent("ann");
    expect(list).toHaveTextContent("L1000011");
    expect(list).toHaveTextContent("2 books out");
    expect(list).toHaveTextContent("0 books out");
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
    expect(findCalls(calls)).toEqual([{ q: "ann" }]);
    expect(calls.every((c) => !c.path.includes("ann"))).toBe(true); // never in an address
  });

  test("nothing is searched while typing: only Enter or Find does", async () => {
    const calls = fakeServer({ ...base, "POST /api/desk/find": matches([ann]) });
    await renderApp("/desk/checkout");
    await userEvent.type(screen.getByLabelText("Patron"), "ann");
    expect(findCalls(calls)).toEqual([]);
    expect(screen.queryByRole("heading", { name: /Matches for/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Find" }));
    expect(await screen.findByRole("heading", { name: "Matches for ‘ann’" })).toBeInTheDocument();
    expect(findCalls(calls)).toEqual([{ q: "ann" }]);
  });

  test("a username finds its owner", async () => {
    const calls = fakeServer({ ...base, "POST /api/desk/find": matches([ann]) });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    expect(await screen.findByRole("button", { name: /Ann Lee/ })).toBeInTheDocument();
    expect(findCalls(calls)).toEqual([{ q: "ann" }]);
  });

  test("one match is only listed: nobody is selected and nothing can be checked out until it's chosen", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([{ ...ann, card: "L2041187" }]) });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    const choice = await screen.findByRole("button", { name: /Ann Lee/ });
    expect(screen.queryByLabelText("Copy barcode")).toBeNull();
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
    await userEvent.click(choice);
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
    expect(screen.getByLabelText("Copy barcode")).toBeInTheDocument();
  });

  test("same name: the username, card and books out tell them apart, and each choice uses its own card", async () => {
    const calls = fakeServer({
      ...base,
      "POST /api/desk/find": matches([
        { ...ann, display_name: "Sam Lee", username: "sam1", card: "L2041187", out: 1 },
        { ...ann, id: 12, display_name: "Sam Lee", username: "sam2", card: "L5555555", out: 3 },
      ]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "sam");
    const [one, two] = (await screen.findAllByRole("button", { name: /Sam Lee/ })) as [HTMLElement, HTMLElement];
    expect(one).toHaveTextContent("sam1");
    expect(one).toHaveTextContent("1 book out");
    expect(two).toHaveTextContent("sam2");
    expect(two).toHaveTextContent("3 books out");
    await userEvent.click(one);
    await screen.findByRole("heading", { name: "Maya Okafor" });
    expect(resolveCalls(calls)).toEqual([{ code: "L2041187", step: "patron" }]);
  });

  test("no match: it says so, with what was searched", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([]) });
    await renderApp("/desk/checkout");
    await scan("Patron", "zzz");
    expect(await screen.findByText("No patron matches ‘zzz’.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Matches/ })).toBeNull();
  });

  test("more than 50 match: the librarian is asked to type more", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([ann], true) });
    await renderApp("/desk/checkout");
    await scan("Patron", "a");
    const note = await screen.findByText("More than 50 patrons match. Type more of the name.");
    // said before the rows, so it's read first
    expect(note.compareDocumentPosition(screen.getByRole("list")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test("exactly 50 match: no such notice", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([ann], false) });
    await renderApp("/desk/checkout");
    await scan("Patron", "a");
    await screen.findByRole("button", { name: /Ann Lee/ });
    expect(screen.queryByText(/More than 50/)).toBeNull();
  });

  test("a card still selects at once, typed or scanned by a handheld, with no list", async () => {
    const calls = fakeServer(base);
    await renderApp("/desk/checkout");
    await scan("Patron", "L2041187");
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /Matches for/ })).toBeNull();
    expect(findCalls(calls)).toEqual([{ q: "L2041187" }]);
    await userEvent.click(screen.getByRole("button", { name: "Next patron" }));
    // a handheld scanner types the code fast and ends with Enter
    await userEvent.type(screen.getByLabelText("Patron"), "L2041187{Enter}", { delay: null });
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
  });

  test("a staff account is a match like any other and can be chosen", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/find": matches([{ ...ann, display_name: "Pat Administrator", username: "admin", card: "L2041187" }]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "pat");
    await userEvent.click(await screen.findByRole("button", { name: /Pat Administrator/ }));
    expect(await screen.findByRole("region", { name: "Patron" })).toBeInTheDocument();
  });

  test("a deactivated account and one with no card are listed with why, and can't be chosen", async () => {
    const calls = fakeServer({
      ...base,
      "POST /api/desk/find": matches([
        { ...ann, display_name: "Dee Gone", username: "dee", active: false },
        { ...ann, id: 12, display_name: "Cy Nocard", username: "cy", card: "" },
      ]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "e");
    const list = (await screen.findByRole("heading", { name: "Matches for ‘e’" })).closest("section")!;
    expect(list).toHaveTextContent("Dee Gone");
    expect(list).toHaveTextContent("Deactivated");
    expect(list).toHaveTextContent("Cy Nocard");
    expect(list).toHaveTextContent("No library card");
    expect(within(list).queryAllByRole("button")).toHaveLength(0);
    await userEvent.click(within(list).getByText("Dee Gone"));
    expect(resolveCalls(calls)).toEqual([]);
  });

  test("the list can be reached and chosen from the keyboard", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([{ ...ann, card: "L2041187" }]) });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    const choice = await screen.findByRole("button", { name: /Ann Lee/ });
    choice.focus();
    await userEvent.keyboard("{Enter}");
    expect(await screen.findByRole("heading", { name: "Maya Okafor" })).toBeInTheDocument();
  });

  test("a user with only the desk permission gets the search", async () => {
    const me = meOf("librarian");
    const calls = fakeServer({
      ...base,
      "GET /api/me": { body: { ...me, permissions: ["circ.desk", "loans.own.view"] } },
      "POST /api/desk/find": matches([ann]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    expect(await screen.findByRole("button", { name: /Ann Lee/ })).toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith("/api/patrons"))).toBe(false);
  });

  test("a new submit clears the old list, and an earlier slower answer never replaces a later one", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    fakeServer({ ...base });
    const real = globalThis.fetch;
    vi.stubGlobal("fetch", (path: string, init?: RequestInit) => {
      if (path !== "/api/desk/find") return real(path, init);
      const q = (JSON.parse(String(init?.body)) as { q: string }).q;
      const body = { kind: "matches", patrons: [{ ...ann, display_name: q === "ann" ? "Ann Lee" : "Bob Ray", id: q === "ann" ? 11 : 12 }], more: false };
      const reply = () => new Response(JSON.stringify(body), { status: 200 });
      return q === "ann" ? gate.then(reply) : Promise.resolve(reply());
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    await scan("Patron", "bob");
    expect(screen.queryByRole("heading", { name: /Matches for/ })).toBeNull();
    release();
    expect(await screen.findByRole("heading", { name: "Matches for ‘bob’" })).toBeInTheDocument();
    expect(screen.queryByText("Ann Lee")).toBeNull();
  });

  test("a choice still waiting when another search is submitted never selects anyone", async () => {
    fakeServer({ ...base, "POST /api/desk/find": (b) => ((b as { q: string }).q === "zzz" ? matches([]) : matches([{ ...ann, card: "L2041187" }])) });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    const choice = await screen.findByRole("button", { name: /Ann Lee/ });
    let release: () => void = () => {};
    const gate = new Promise<void>((r) => (release = r));
    const real = globalThis.fetch;
    vi.stubGlobal("fetch", (path: string, init?: RequestInit) => (path === "/api/desk/resolve" ? gate.then(() => real(path, init)) : real(path, init)));
    await userEvent.click(choice);
    await scan("Patron", "zzz"); // a later submit clears the list and wins
    release();
    expect(await screen.findByText("No patron matches ‘zzz’.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
  });

  test("choosing a card that was re-issued meanwhile says so and keeps the list", async () => {
    fakeServer({
      ...base,
      "POST /api/desk/find": matches([ann]),
    });
    await renderApp("/desk/checkout");
    await scan("Patron", "ann");
    await userEvent.click(await screen.findByRole("button", { name: /Ann Lee/ }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Card not read");
    expect(alert).toHaveTextContent("No such patron");
    expect(screen.getByRole("button", { name: /Ann Lee/ })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
  });

  test("a refused search shows the server's message", async () => {
    fakeServer({ ...base, "POST /api/desk/find": apiError(422, "invalid", "Search for something shorter than 200 characters.", "q") });
    await renderApp("/desk/checkout");
    await scan("Patron", "x");
    expect(await screen.findByRole("alert")).toHaveTextContent("Search for something shorter than 200 characters.");
  });

  test("a dropped connection says the search couldn't be done, and selects no one", async () => {
    fakeServer(base);
    await renderApp("/desk/checkout");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await scan("Patron", "ann");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The search couldn't be done");
    expect(alert).toHaveTextContent("Try again");
    expect(screen.queryByRole("region", { name: "Patron" })).toBeNull();
  });

  test("names are shown as text, never as markup", async () => {
    fakeServer({ ...base, "POST /api/desk/find": matches([{ ...ann, display_name: "<img src=x onerror=alert(1)>" }]) });
    await renderApp("/desk/checkout");
    await scan("Patron", "img");
    expect(await screen.findByText("<img src=x onerror=alert(1)>")).toBeInTheDocument();
    expect(document.querySelector("img[src='x']")).toBeNull();
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
    await scan("Patron", "L2041187");
    await waitFor(() => expect(screen.getByRole("heading", { name: "This checkout · 0 books" })).toBeInTheDocument());
    await userEvent.click(screen.getByRole("button", { name: "Next patron" }));
    expect(screen.getByLabelText("Patron")).toBeInTheDocument();
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
    expect(screen.queryByLabelText("Patron")).toBeNull();
  });
});
