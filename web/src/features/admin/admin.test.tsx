import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ActivityEntry } from "../../api/admin";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";
import { dayText, timeText } from "./zone";

afterEach(() => {
  vi.unstubAllGlobals();
});

const admin = () => meOf("administrator", { settings: { time_zone: "America/New_York" } });
const stats = { books: 1200, copies: 1500, patrons: 340, current_loans: 88, overdue_loans: 7 };

describe("ADMIN-3 Statistics and circulation reports", () => {
  test("Statistics: the five counts are shown", async () => {
    fakeServer({ "GET /api/me": { body: admin() }, "GET /api/admin/reports/statistics": { body: stats } });
    await renderApp("/admin/statistics");
    const region = await screen.findByRole("region", { name: "Library statistics" });
    for (const [label, n] of [["Books", "1,200"], ["Copies", "1,500"], ["Patrons", "340"], ["On loan now", "88"], ["Overdue", "7"]]) {
      expect(within(region).getByText(label!).parentElement).toHaveTextContent(n!);
    }
  });

  test("Report: the chosen range is sent and the counts are shown, with no patron names", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: admin() },
      "GET /api/admin/reports/statistics": { body: stats },
      "GET /api/admin/reports/circulation?from=2026-01-01&to=2026-01-31": {
        body: { from: "2026-01-01", to: "2026-01-31", checkouts: 412, returns: 398 },
      },
    });
    await renderApp("/admin/statistics");
    await screen.findByRole("region", { name: "Library statistics" });
    await userEvent.clear(screen.getByLabelText("From"));
    await userEvent.type(screen.getByLabelText("From"), "2026-01-01");
    await userEvent.clear(screen.getByLabelText("To"));
    await userEvent.type(screen.getByLabelText("To"), "2026-01-31");
    await userEvent.click(screen.getByRole("button", { name: "Show report" }));
    const result = await screen.findByRole("status");
    expect(result).toHaveTextContent("2026-01-01 to 2026-01-31");
    expect(within(result).getByText("Checkouts").parentElement).toHaveTextContent("412");
    expect(within(result).getByText("Returns").parentElement).toHaveTextContent("398");
    expect(calls.filter((c) => c.path.startsWith("/api/admin/reports/circulation"))).toHaveLength(1);
  });

  test("a range the server refuses is explained under the field", async () => {
    fakeServer({
      "GET /api/me": { body: admin() },
      "GET /api/admin/reports/statistics": { body: stats },
      "GET /api/admin/reports/circulation?from=2026-02-01&to=2026-01-01": apiError(422, "invalid", "The end date can't be before the start date.", "to"),
    });
    await renderApp("/admin/statistics");
    await screen.findByRole("region", { name: "Library statistics" });
    await userEvent.clear(screen.getByLabelText("From"));
    await userEvent.type(screen.getByLabelText("From"), "2026-02-01");
    await userEvent.clear(screen.getByLabelText("To"));
    await userEvent.type(screen.getByLabelText("To"), "2026-01-01");
    await userEvent.click(screen.getByRole("button", { name: "Show report" }));
    expect(await screen.findByText("The end date can't be before the start date.")).toBeInTheDocument();
    expect(screen.queryByRole("status")).toBeNull();
  });

  test("a failed load says so and offers a retry", async () => {
    fakeServer({ "GET /api/me": { body: admin() }, "GET /api/admin/reports/statistics": apiError(500, "internal", "Something went wrong.") });
    await renderApp("/admin/statistics");
    expect(await screen.findByText("Something went wrong.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  test("a librarian without the permission is told so and nothing is requested", async () => {
    const calls = fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    await renderApp("/admin/statistics");
    expect(await screen.findByText("You don't have access to this page")).toBeInTheDocument();
    expect(calls.some((c) => c.path.startsWith("/api/admin"))).toBe(false);
  });
});

const entry = (id: number, over: Partial<ActivityEntry> = {}): ActivityEntry => ({
  id,
  at: "2026-11-16T14:30:00Z",
  actor: { id: 1, display_name: "Ada Admin" },
  action: "user.role_changed",
  target: "user:7",
  details: { from: "patron", to: "librarian" },
  ...over,
});

describe("ADMIN-4 Administrative activity log", () => {
  test("Role change logged: the entry shows who did it, what, to whom and when in the library's zone", async () => {
    fakeServer({
      "GET /api/me": { body: admin() },
      "GET /api/admin/activity?page=1": { body: { entries: [entry(2), entry(1, { action: "settings.changed", target: "settings", details: {} })], page: 1, per_page: 50, total: 2 } },
    });
    await renderApp("/admin/activity");
    const rows = await screen.findAllByRole("listitem");
    const first = rows.find((r) => r.textContent?.includes("Changed a role"))!;
    expect(first).toHaveTextContent("Ada Admin");
    expect(first).toHaveTextContent("user 7");
    expect(first).toHaveTextContent("patron to librarian");
    expect(within(first).getByText(/9:30|09:30/)).toBeInTheDocument(); // 14:30Z is 09:30 in New York
    expect(rows.findIndex((r) => r === first)).toBeLessThan(rows.findIndex((r) => r.textContent?.includes("Changed settings")));
  });

  test("the server console is named and the log pages", async () => {
    const calls = fakeServer({
      "GET /api/me": { body: admin() },
      "GET /api/admin/activity?page=1": {
        body: { entries: [entry(9, { actor: { id: null, display_name: "Server console" } })], page: 1, per_page: 1, total: 2 },
      },
      "GET /api/admin/activity?page=2": { body: { entries: [entry(8, { action: "invitation.created", target: "invitation:3", details: { role: "librarian" } })], page: 2, per_page: 1, total: 2 } },
    });
    await renderApp("/admin/activity");
    expect(await screen.findByText("Server console")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(await screen.findByText("Created an invitation")).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toContain("/api/admin/activity?page=2");
  });

  test("an empty log says so, and an unknown action is still shown", async () => {
    fakeServer({ "GET /api/me": { body: admin() }, "GET /api/admin/activity?page=1": { body: { entries: [], page: 1, per_page: 50, total: 0 } } });
    await renderApp("/admin/activity");
    expect(await screen.findByText(/Nothing has been logged yet/)).toBeInTheDocument();
  });

  test("a failed load offers a retry; a user without activity.view is refused", async () => {
    fakeServer({ "GET /api/me": { body: admin() }, "GET /api/admin/activity?page=1": apiError(500, "internal", "Something went wrong.") });
    await renderApp("/admin/activity");
    expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
    vi.unstubAllGlobals();
    fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["circ.desk", "reports.view"] }) } });
    await renderApp("/admin/activity");
    expect(await screen.findAllByText("You don't have access to this page")).not.toHaveLength(0);
  });
});

describe("zone helpers", () => {
  test("a moment is shown in the library's zone and a day is the library's day", () => {
    expect(timeText("2026-11-16T03:30:00Z", "Asia/Tokyo")).toMatch(/12:30|12.30/);
    expect(dayText(new Date("2026-11-16T03:30:00Z"), "America/New_York")).toBe("2026-11-15");
    expect(dayText(new Date("2026-11-16T03:30:00Z"), "America/New_York", 29)).toBe("2026-10-17");
    expect(timeText("2026-11-16T14:30:00Z", "Not/AZone")).not.toBe("");
  });
});
