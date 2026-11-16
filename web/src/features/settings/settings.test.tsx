import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AdminSettings, BrandingCheck } from "../../api/setup";
import { apiError, fakeServer, library, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const hearth = { "--wood": "#3B2A1F", "--primary": "#7A2E22", "--brass": "#C08A2E", "--paper": "#F7F0E3" };
const inkwell = { "--wood": "#1F2A3A", "--primary": "#2C4A6E", "--brass": "#C9A15A", "--paper": "#F3F1EC" };
const FINGERPRINT = "AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89";

const start: AdminSettings = {
  ...library,
  accent: "",
  colours: hearth,
  schemes: [
    { id: "hearth", name: "Hearth", description: "Warm leather & cream · default", colours: hearth },
    { id: "inkwell", name: "Inkwell", description: "Navy & parchment", colours: inkwell },
  ],
  time_zone: "Europe/London",
  loan_period_days: 14,
  max_loans: 5,
  max_renewals: 2,
  session_idle_minutes: 43200,
  self_checkout: false,
  copy_id_mode: "isbn",
  ca_fingerprint: FINGERPRINT,
};

const passes: BrandingCheck = { ok: true, failed: [], on_accent_ratio: 9, as_text_ratio: 9, suggestion: "", colours: hearth };
const type = (label: string, text: string) => userEvent.type(screen.getByLabelText(label), text);

/** A server that keeps what is saved, so a second visit shows whether it persisted. */
function server(extra: Parameters<typeof fakeServer>[0] = {}) {
  let now: AdminSettings = start;
  const calls = fakeServer({
    "GET /api/me": { body: meOf("administrator") },
    "GET /api/admin/settings": () => ({ body: now }),
    "GET /api/library": () => ({ body: { ...library, name: now.name, public_catalog: now.public_catalog } }),
    "POST /api/admin/branding/check": { body: passes },
    "PUT /api/admin/settings": (body) => ((now = { ...now, ...(body as object) }), { body: now }),
    "PUT /api/admin/branding": (body) => ((now = { ...now, ...(body as object) }), { body: now }),
    "PUT /api/admin/copy-id-mode": (body) => ((now = { ...now, copy_id_mode: (body as { mode: AdminSettings["copy_id_mode"] }).mode }), { body }),
    ...extra,
  });
  return calls;
}

const puts = (calls: ReturnType<typeof server>) => calls.filter((c) => c.method !== "GET" && !c.path.endsWith("/check"));
const section = (name: string) => within(screen.getByRole("region", { name }));

async function open() {
  await renderApp("/admin/settings");
  await screen.findByRole("heading", { name: "Settings" });
}

describe("ADMIN-1 Name, logo and colour scheme", () => {
  test("Rename: the new name is saved and shown", async () => {
    const calls = server();
    await open();
    await userEvent.clear(screen.getByLabelText("Library name"));
    await type("Library name", "Maple Street Reading Room");
    await userEvent.click(screen.getByRole("button", { name: "Save name and look" }));
    expect(await screen.findByText("Saved.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([
      { method: "PUT", path: "/api/admin/branding", body: { name: "Maple Street Reading Room", scheme: "hearth", accent: "" } },
    ]);
    // The app's own name follows: the library was read again.
    await waitFor(() => expect(screen.getAllByText("Maple Street Reading Room").length).toBeGreaterThan(0));
  });

  test("Preset scheme: choosing one draws the preview from the server's colours and saves it", async () => {
    const calls = server({
      "POST /api/admin/branding/check": (body) =>
        (body as { scheme: string }).scheme === "inkwell" ? { body: { ...passes, colours: inkwell } } : { body: passes },
    });
    await open();
    await userEvent.click(screen.getByRole("radio", { name: /Inkwell/ }));
    await waitFor(() => expect(screen.getByTestId("preview").style.getPropertyValue("--primary")).toBe("#2C4A6E"));
    await userEvent.click(screen.getByRole("button", { name: "Save name and look" }));
    await screen.findByText("Saved.");
    expect(puts(calls)).toMatchObject([{ path: "/api/admin/branding", body: { scheme: "inkwell" } }]);
  });

  test("an accent that is unreadable is refused with the suggestion, and nothing is saved", async () => {
    const calls = server({
      "POST /api/admin/branding/check": {
        body: { ok: false, failed: ["on_accent"], on_accent_ratio: 1.9, as_text_ratio: 3.2, suggestion: "#8A5A12", colours: hearth },
      },
    });
    await open();
    await type("Accent colour (optional)", "#E8B94A");
    const alert = await screen.findByText("Text would be hard to read on this colour");
    expect(alert.closest("[role=alert]")).toHaveTextContent("#8A5A12");
    expect(screen.getByRole("button", { name: "Save name and look" })).toBeDisabled();
    expect(puts(calls)).toEqual([]);
  });

  test("Bad logo: the server's message is shown and the logo is unchanged", async () => {
    server({ "PUT /api/admin/branding/logo": apiError(422, "invalid", "The logo must be a JPEG, PNG or WebP image of at most 2 MB.", "logo") });
    await open();
    await userEvent.upload(screen.getByLabelText("Logo file"), new File(["x"], "logo.gif", { type: "image/gif" }), { applyAccept: false });
    expect(await screen.findByRole("alert")).toHaveTextContent("The logo must be a JPEG, PNG or WebP image of at most 2 MB.");
    expect(screen.getByText("Using the default logo")).toBeInTheDocument();
  });

  test("a logo is uploaded, and can be removed", async () => {
    const withLogo = { ...start, logo_url: "/media/logo.png" };
    const calls = server({
      "PUT /api/admin/branding/logo": { body: withLogo },
      "DELETE /api/admin/branding/logo": { body: start },
    });
    await open();
    await userEvent.upload(screen.getByLabelText("Logo file"), new File(["x"], "logo.png", { type: "image/png" }));
    expect(await screen.findByText("Your logo")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("Using the default logo")).toBeInTheDocument();
    expect(puts(calls).map((c) => `${c.method} ${c.path}`)).toEqual(["PUT /api/admin/branding/logo", "DELETE /api/admin/branding/logo"]);
  });
});

describe("CIRC-4 Borrowing policy", () => {
  test("the loan period, limits and time zone are saved, and the saved ones are shown", async () => {
    const calls = server();
    await open();
    expect(screen.getByLabelText("Time zone")).toHaveValue("Europe/London");
    await userEvent.clear(screen.getByLabelText("Loan period (days)"));
    await type("Loan period (days)", "21");
    await userEvent.clear(screen.getByLabelText("Books out at once"));
    await type("Books out at once", "8");
    await userEvent.clear(screen.getByLabelText("Renewals"));
    await type("Renewals", "0");
    await userEvent.click(screen.getByRole("button", { name: "Save borrowing rules" }));
    expect(await section("Borrowing rules").findByText("Saved.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([
      { method: "PUT", path: "/api/admin/settings", body: { time_zone: "Europe/London", loan_period_days: 21, max_loans: 8, max_renewals: 0 } },
    ]);
  });

  test("a value the server refuses is said under its field", async () => {
    server({ "PUT /api/admin/settings": apiError(422, "invalid", "The loan period must be 1 to 365 days.", "loan_period_days") });
    await open();
    await userEvent.clear(screen.getByLabelText("Loan period (days)"));
    await type("Loan period (days)", "999");
    await userEvent.click(screen.getByRole("button", { name: "Save borrowing rules" }));
    expect(await screen.findByText("The loan period must be 1 to 365 days.")).toBeInTheDocument();
    expect(screen.queryByText("Saved.")).toBeNull();
  });
});

describe("Sign-in timeout", () => {
  test("is saved in minutes", async () => {
    const calls = server();
    await open();
    await userEvent.clear(screen.getByLabelText("Sign out after (minutes unused)"));
    await type("Sign out after (minutes unused)", "120");
    await userEvent.click(screen.getByRole("button", { name: "Save timeout" }));
    expect(await section("Signing in").findByText("Saved.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([{ method: "PUT", path: "/api/admin/settings", body: { session_idle_minutes: 120 } }]);
  });

  test("a refusal from the server is said under the field; a non-number is said before sending", async () => {
    const calls = server({ "PUT /api/admin/settings": apiError(422, "invalid", "Sign-out time must be 15 minutes to 30 days.", "session_idle_minutes") });
    await open();
    await userEvent.clear(screen.getByLabelText("Sign out after (minutes unused)"));
    await type("Sign out after (minutes unused)", "soon");
    await userEvent.click(screen.getByRole("button", { name: "Save timeout" }));
    expect(screen.getByText("Enter a whole number of minutes.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([]);
    await userEvent.clear(screen.getByLabelText("Sign out after (minutes unused)"));
    await type("Sign out after (minutes unused)", "5");
    await userEvent.click(screen.getByRole("button", { name: "Save timeout" }));
    expect(await screen.findByText("Sign-out time must be 15 minutes to 30 days.")).toBeInTheDocument();
  });
});

describe("CIRC-7 Self-checkout option", () => {
  test("it is off, and switching it on is saved and still on when the page is opened again", async () => {
    const calls = server();
    await open();
    const sw = screen.getByRole("switch", { name: /Self-checkout/ });
    expect(sw).not.toBeChecked();
    expect(screen.getByText(/check out an available copy to themselves/)).toBeInTheDocument();
    await userEvent.click(sw);
    expect(await screen.findByText("Self-checkout is now on.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([{ method: "PUT", path: "/api/admin/settings", body: { self_checkout: true } }]);
    cleanup();
    await open();
    expect(screen.getByRole("switch", { name: /Self-checkout/ })).toBeChecked();
  });

  test("a refusal leaves the switch where it was and says why", async () => {
    server({ "PUT /api/admin/settings": apiError(403, "forbidden", "You can't change settings.") });
    await open();
    await userEvent.click(screen.getByRole("switch", { name: /Self-checkout/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("You can't change settings.");
    expect(screen.getByRole("switch", { name: /Self-checkout/ })).not.toBeChecked();
  });
});

describe("ACCESS-4 Optional public catalog", () => {
  test("it is off, and switching it on is saved and still on when the page is opened again", async () => {
    const calls = server();
    await open();
    const sw = screen.getByRole("switch", { name: /Public catalog/ });
    expect(sw).not.toBeChecked();
    expect(screen.getByText(/never see patrons, who has a loan, or loan history/)).toBeInTheDocument();
    await userEvent.click(sw);
    expect(await screen.findByText("Public catalog is now on.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([{ method: "PUT", path: "/api/admin/settings", body: { public_catalog: true } }]);
    // The app was told, so the visitor guard follows.
    expect(calls.filter((c) => c.path === "/api/library").length).toBeGreaterThan(1);
    cleanup();
    await open();
    expect(screen.getByRole("switch", { name: /Public catalog/ })).toBeChecked();
  });
});

describe("CAT-9 How copies are identified", () => {
  test("Switch to own barcodes: it asks first, saves nothing until confirmed, then saves", async () => {
    const calls = server();
    await open();
    const copyIds = section("How copies are identified");
    expect(copyIds.getByText("By ISBN")).toBeInTheDocument();
    await userEvent.click(copyIds.getByRole("button", { name: "Switch to own barcodes" }));
    const ask = screen.getByRole("alertdialog");
    expect(ask).toHaveTextContent("Every copy that has no barcode yet is given a generated one");
    expect(ask).toHaveTextContent("All copies and loans are kept");
    expect(puts(calls)).toEqual([]);

    await userEvent.click(within(ask).getByRole("button", { name: "Yes, switch to own barcodes" }));
    expect(await copyIds.findByText("Copies are now identified by own barcode.")).toBeInTheDocument();
    expect(puts(calls)).toEqual([{ method: "PUT", path: "/api/admin/copy-id-mode", body: { mode: "own" } }]);
    expect(copyIds.getByText("By own barcode")).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).toBeNull();
    // And back again explains the other direction.
    await userEvent.click(copyIds.getByRole("button", { name: "Switch to ISBN scanning" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Every copy keeps its barcode, and scanning one still finds that copy");
  });

  test("Cancel leaves the mode alone", async () => {
    const calls = server();
    await open();
    await userEvent.click(screen.getByRole("button", { name: "Switch to own barcodes" }));
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(puts(calls)).toEqual([]);
  });

  test("a failed switch says why and stays asking", async () => {
    server({ "PUT /api/admin/copy-id-mode": apiError(500, "internal", "Something went wrong (request 4f2).") });
    await open();
    await userEvent.click(screen.getByRole("button", { name: "Switch to own barcodes" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, switch to own barcodes" }));
    expect(await within(screen.getByRole("alertdialog")).findByRole("alert")).toHaveTextContent("request 4f2");
    expect(screen.getByText("By ISBN")).toBeInTheDocument();
  });
});

describe("HTTPS section", () => {
  test("shows the root fingerprint, says to compare it with the device's own display and to read it from a trusting device", async () => {
    server();
    await open();
    expect(screen.getByTestId("fingerprint")).toHaveTextContent(FINGERPRINT);
    const https = section("Secure connection (HTTPS)");
    expect(https.getByRole("link", { name: "trust guide" })).toHaveAttribute("href", expect.stringMatching(/^http:\/\/[^/]+:8080\/trust\/$/));
    expect(https.getByText(/SHA-256 fingerprint that device shows/)).toBeInTheDocument();
    expect(https.getByText(/only from a device that already trusts the library, never from the one you are setting up/)).toBeInTheDocument();
    // The guide is not a source to compare with: nothing here says to.
    expect(https.queryByText(/compare.*with the (trust )?guide/i)).toBeNull();
  });

  test("a library with no certificate authority of its own has no fingerprint to show", async () => {
    server({ "GET /api/admin/settings": { body: { ...start, ca_fingerprint: undefined } } });
    await open();
    expect(screen.queryByTestId("fingerprint")).toBeNull();
    expect(screen.queryByRole("link", { name: "trust guide" })).toBeNull();
    expect(screen.getByText(/no certificate authority of its own/)).toBeInTheDocument();
  });
});

describe("access", () => {
  test("a librarian is told they have no access, and nothing is asked of the settings route", async () => {
    const calls = fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    await renderApp("/admin/settings");
    expect(await screen.findByText("You don't have access to this page")).toBeInTheDocument();
    expect(calls.some((c) => c.path === "/api/admin/settings")).toBe(false);
  });

  test("a failed load shows a retry, and retrying loads it", async () => {
    let fail = true;
    server({ "GET /api/admin/settings": () => (fail ? apiError(500, "internal", "Something went wrong (request 9).") : { body: start }) });
    await renderApp("/admin/settings");
    expect(await screen.findByRole("alert")).toHaveTextContent("request 9");
    fail = false;
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("heading", { name: "Settings" })).toBeInTheDocument();
  });
});
