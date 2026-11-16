import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { CheckinResult, DeskLoan } from "../../api/desk";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

function loan(over: Partial<DeskLoan> = {}): DeskLoan {
  return {
    id: 1,
    copy_id: 10,
    copy_number: 1,
    copy_code: "C-000033",
    book_id: 5,
    title: "The Secret Garden",
    authors: ["F. H. Burnett"],
    location: "Living room · Shelf B2",
    patron_id: 7,
    patron_name: "Maya Okafor",
    due_date: "2026-11-13",
    days_late: 0,
    overdue: false,
    ...over,
  };
}

const returned = (l: DeskLoan): { body: CheckinResult } => ({ body: { status: "returned", message: "Checked in.", loan: l } });

async function open(handler: Parameters<typeof fakeServer>[0]["POST /api/desk/checkin"]) {
  const calls = fakeServer({ "GET /api/me": { body: meOf("librarian") }, "POST /api/desk/checkin": handler! });
  await renderApp("/desk/checkin");
  return calls;
}

const scan = (text: string) => userEvent.type(screen.getByRole("textbox"), `${text}{Enter}`);
const list = () => within(screen.getByRole("region", { name: /This session/ }));

describe("CIRC-2 Check in", () => {
  test("Return: the copy is shown as back, with where it goes on the shelf, and listed", async () => {
    const calls = await open(() => returned(loan()));
    await scan("C-000033");
    const latest = await screen.findByRole("status", { name: "Latest scan" });
    expect(latest).toHaveTextContent("Checked in");
    expect(latest).toHaveTextContent("The Secret Garden");
    expect(latest).toHaveTextContent("borrowed by Maya Okafor");
    expect(latest).toHaveTextContent("Put it back: Living room · Shelf B2");
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ code: "C-000033" });
    expect(list().getByText("On time")).toBeInTheDocument();
    expect(screen.getByText("This session · 1 scan")).toBeInTheDocument();
    // continuous scanning: the field is empty and ready
    expect(screen.getByRole("textbox")).toHaveValue("");
    expect(screen.getByRole("textbox")).toHaveFocus();
  });

  test("Return: a late copy says how many days late, large and in the list", async () => {
    await open(() => returned(loan({ days_late: 3, overdue: true })));
    await scan("C-000033");
    expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("Checked in · returned 3 days late");
    expect(screen.getByRole("status", { name: "Latest scan" })).toHaveTextContent("was due Fri 13 Nov");
    expect(list().getByText("3 days late")).toBeInTheDocument();
  });

  test("Return: one day late is not '1 days'", async () => {
    await open(() => returned(loan({ days_late: 1 })));
    await scan("C-000033");
    expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("returned 1 day late");
  });

  test("Single copy out by ISBN: scanning the ISBN returns that loan with no choice", async () => {
    const calls = await open(() => returned(loan()));
    await scan("9780141439518");
    expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("Checked in");
    expect(screen.queryByRole("button", { name: /^Return copy/ })).toBeNull();
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ code: "9780141439518" });
  });

  describe("Several out by ISBN", () => {
    const choose = {
      body: {
        status: "choose",
        message: "2 copies of The Secret Garden are out. Scan the patron's card, or choose which loan is being returned.",
        choices: [loan({ id: 1, copy_number: 1, patron_name: "Maya Okafor" }), loan({ id: 3, copy_number: 3, patron_name: "Sam Lee", patron_id: 8 })],
      } satisfies CheckinResult,
    };

    test("the choice shows patron and copy number, and only the chosen loan is returned", async () => {
      const calls = await open((body) => {
        const b = body as { loan_id?: number };
        return b.loan_id === 3 ? returned(loan({ id: 3, copy_number: 3, patron_name: "Sam Lee" })) : choose;
      });
      await scan("9780141439518");
      expect(await screen.findByText(/2 copies of The Secret Garden are out/)).toBeInTheDocument();
      expect(screen.getByText("Maya Okafor · Copy 1")).toBeInTheDocument();
      expect(screen.getByText("Sam Lee · Copy 3")).toBeInTheDocument();
      // nothing is returned until one is chosen
      expect(screen.queryByRole("status", { name: "Latest scan" })).toBeNull();
      expect(list().getByText("Nothing scanned yet.")).toBeInTheDocument();

      await userEvent.click(screen.getByRole("button", { name: "Return copy 3 from Sam Lee" }));
      expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("from Sam Lee".replace("from ", "borrowed by "));
      const posts = calls.filter((c) => c.method === "POST");
      expect(posts.at(-1)?.body).toEqual({ code: "9780141439518", loan_id: 3 });
      expect(posts).toHaveLength(2);
      // the choice is closed
      expect(screen.queryByText("Maya Okafor · Copy 1")).toBeNull();
    });

    test("scanning the patron's card narrows it to that patron's loan", async () => {
      const calls = await open((body) =>
        (body as { card?: string }).card === "123456789012" ? returned(loan({ id: 1, patron_name: "Maya Okafor" })) : choose,
      );
      await scan("9780141439518");
      await screen.findByText("Sam Lee · Copy 3");
      expect(screen.getByLabelText("Patron's card")).toBeInTheDocument();
      await scan("123456789012");
      expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("borrowed by Maya Okafor");
      expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ code: "9780141439518", card: "123456789012" });
      expect(screen.queryByText("Sam Lee · Copy 3")).toBeNull();
    });

    test("a card that doesn't have the book out says so, and the choice stays open", async () => {
      await open((body) =>
        (body as { card?: string }).card ? apiError(409, "conflict", "Lee doesn't have The Secret Garden out. Choose a loan from the list.") : choose,
      );
      await scan("9780141439518");
      await screen.findByText("Sam Lee · Copy 3");
      await scan("555555555555");
      expect(await screen.findByRole("alert")).toHaveTextContent("Choose a loan from the list.");
      expect(screen.getByText("Sam Lee · Copy 3")).toBeInTheDocument();
    });

    test("the scan field has focus again after Return, after Cancel and after a card scan", async () => {
      const handler = (body: unknown) => {
        const b = body as { loan_id?: number; card?: string };
        return b.loan_id || b.card ? returned(loan({ id: 3 })) : choose;
      };
      await open(handler);
      const field = () => screen.getByRole("textbox");
      await scan("9780141439518");
      await userEvent.click(await screen.findByRole("button", { name: "Return copy 3 from Sam Lee" }));
      await screen.findByRole("status", { name: "Latest scan" });
      expect(field()).toHaveFocus();
      await scan("9780141439518");
      await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
      expect(field()).toHaveFocus();
      await scan("9780141439518");
      await screen.findByText("Sam Lee · Copy 3");
      await scan("123456789012");
      await screen.findByText("This session · 2 scans");
      expect(field()).toHaveFocus();
    });

    test("the earlier result is not shown while a choice is open, and returns when it is settled", async () => {
      await open((body) => ((body as { code: string }).code === "NOPE" ? apiError(404, "not_found", "Nothing matches that code.") : choose));
      await scan("NOPE");
      expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("Nothing matches that code");
      await scan("9780141439518");
      await screen.findByText("Sam Lee · Copy 3");
      expect(screen.queryByRole("status", { name: "Latest scan" })).toBeNull();
      await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
      expect(screen.getByRole("status", { name: "Latest scan" })).toHaveTextContent("Nothing matches that code");
    });

    test("scanning another book while a choice is open checks that book in, never 'no such patron'", async () => {
      const calls = await open((body) => {
        const b = body as { code: string; card?: string };
        if (b.card) return apiError(404, "not_found", "No such patron: no library card has that number.");
        return b.code === "9781635575637" ? returned(loan({ title: "Piranesi", patron_name: "Sam Lee" })) : choose;
      });
      await scan("9780141439518");
      await screen.findByText("Sam Lee · Copy 3");
      await scan("9781635575637");
      expect(await screen.findByRole("status", { name: "Latest scan" })).toHaveTextContent("Piranesi");
      expect(screen.queryByText("Sam Lee · Copy 3")).toBeNull();
      expect(screen.queryByRole("alert")).toBeNull();
      expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ code: "9781635575637" });
      expect(screen.getByRole("textbox")).toHaveFocus();
    });

    test("Cancel closes the choice and returns nothing", async () => {
      const calls = await open(() => choose);
      await scan("9780141439518");
      await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
      expect(screen.queryByText("Sam Lee · Copy 3")).toBeNull();
      expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
      expect(screen.getByLabelText("Copy barcode or ISBN")).toBeInTheDocument();
    });
  });

  test("Nothing out: they are told none is checked out", async () => {
    await open(() => ({
      body: { status: "not_out", message: "None is checked out: no copy of Piranesi is on loan.", book: { id: 2, title: "Piranesi" } } satisfies CheckinResult,
    }));
    await scan("9781635575637");
    const latest = await screen.findByRole("status", { name: "Latest scan" });
    expect(latest).toHaveTextContent("None is checked out");
    expect(latest).toHaveTextContent("Piranesi");
    expect(list().getByText("None out")).toBeInTheDocument();
  });

  test("a copy that wasn't on loan is listed as already in, with nothing changed", async () => {
    await open(() => ({
      body: {
        status: "not_out",
        message: "x",
        copy: { id: 9, number: 2, code: "C-000122", title: "Charlotte's Web", location: "" },
      } satisfies CheckinResult,
    }));
    await scan("C-000122");
    await screen.findByRole("status", { name: "Latest scan" });
    expect(list().getByText("Already in")).toBeInTheDocument();
    expect(list().getByText(/wasn't checked out, nothing changed/)).toBeInTheDocument();
  });
});

describe("SCAN-3 Scan copy barcode", () => {
  test("Unknown code: the librarian is told nothing matches, and the scan is listed with its own label", async () => {
    await open(() => apiError(404, "not_found", "Nothing matches that code. Check the label, or type it again."));
    await scan("C-009918");
    const latest = await screen.findByRole("status", { name: "Latest scan" });
    expect(latest).toHaveTextContent("Nothing matches that code");
    expect(latest).toHaveTextContent("C-009918");
    expect(list().getByText("Not found")).toBeInTheDocument();
  });

  test("Typed code: a code typed and sent with the button is checked in like a scan", async () => {
    const calls = await open(() => returned(loan()));
    await userEvent.type(screen.getByRole("textbox"), "  c-000033 ");
    await userEvent.click(screen.getByRole("button", { name: "Check in" }));
    await screen.findByRole("status", { name: "Latest scan" });
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ code: "c-000033" });
  });

  test("a server failure is said where the scan was, and is not listed as an outcome", async () => {
    await open(() => apiError(500, "internal", "Something went wrong (request abc)."));
    await scan("C-000033");
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.queryByRole("status", { name: "Latest scan" })).toBeNull();
    expect(screen.getByText("This session · 0 scans")).toBeInTheDocument();
  });
});

describe("CIRC-6 Overdue", () => {
  test("every outcome has its own label and icon, newest first, and Clear empties the list", async () => {
    const replies: Record<string, unknown> = {
      A: returned(loan({ title: "Piranesi", days_late: 0 })),
      B: returned(loan({ title: "Late One", days_late: 3 })),
      C: apiError(404, "not_found", "Nothing matches that code."),
      D: { body: { status: "not_out", message: "x", copy: { id: 1, number: 1, code: "C-1", title: "Charlotte's Web", location: "" } } },
    };
    await open((body) => replies[(body as { code: string }).code] as never);
    for (const c of ["A", "B", "C", "D"]) await scan(c);
    await screen.findByText("This session · 4 scans");
    const items = list().getAllByRole("listitem");
    expect(items).toHaveLength(4);
    expect(items.map((li) => li.querySelector(".ci-badge")?.textContent)).toEqual(["Already in", "Not found", "3 days late", "On time"]);
    // each outcome has an icon of its own
    expect(new Set(items.map((li) => li.querySelector(".ci-dot svg path, .ci-dot svg circle")?.outerHTML)).size).toBe(4);
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByText("This session · 0 scans")).toBeInTheDocument();
  });
});

test("a user without the desk permission is not shown the page", async () => {
  fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["catalog.browse", "patrons.lookup", "catalog.manage"] }) } });
  await renderApp("/desk/checkin");
  expect(screen.getByText("You don't have access to this page")).toBeInTheDocument();
});
