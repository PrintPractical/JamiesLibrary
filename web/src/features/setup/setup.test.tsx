import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AdminSettings, BrandingCheck } from "../../api/setup";
import { apiError, fakeServer, library, meOf, renderApp, signedOut } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const hearth = { "--wood": "#3B2A1F", "--primary": "#7A2E22", "--brass": "#C08A2E", "--paper": "#F7F0E3" };
const inkwell = { "--wood": "#1F2A3A", "--primary": "#2C4A6E", "--brass": "#C9A15A", "--paper": "#F3F1EC" };

const settings: AdminSettings = {
  ...library,
  accent: "",
  colours: hearth,
  schemes: [
    { id: "hearth", name: "Hearth", description: "Warm leather & cream · default", colours: hearth },
    { id: "inkwell", name: "Inkwell", description: "Navy & parchment", colours: inkwell },
  ],
  time_zone: "UTC",
  loan_period_days: 14,
  max_loans: 5,
  max_renewals: 2,
  session_idle_minutes: 43200,
  self_checkout: false,
  copy_id_mode: "isbn",
};

const passes: BrandingCheck = { ok: true, failed: [], on_accent_ratio: 9, as_text_ratio: 9, suggestion: "", colours: hearth };
const type = (label: string, text: string) => userEvent.type(screen.getByLabelText(label), text);

/** A server where the administrator is already signed in, for steps 2 to 4. */
const signedInServer = (extra: Parameters<typeof fakeServer>[0] = {}) =>
  fakeServer({
    "GET /api/me": { body: meOf("administrator") },
    "GET /api/admin/settings": { body: settings },
    "POST /api/admin/branding/check": { body: passes },
    ...extra,
  });

describe("DEPLOY-3 First administrator: step 1", () => {
  const fill = async () => {
    await type("Setup code", "AAAA-BBBB-CCCC-DDDD");
    await type("Your name", "Jamie");
    await type("Username", "jamie");
    await type("Password", "a long enough password");
  };

  test("Owner sets up: the account is created with the code and the wizard goes on to step 2", async () => {
    let signedIn = false;
    const calls = signedInServer({
      "GET /api/me": () => (signedIn ? { body: meOf("administrator") } : signedOut()),
      "GET /api/setup/status": { body: { open: true } },
      "POST /api/setup/admin": () => {
        signedIn = true;
        return { status: 200, body: meOf("administrator") };
      },
    });
    await renderApp("/setup");
    await screen.findByLabelText("Setup code");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account and continue" }));
    expect(await screen.findByRole("heading", { name: "Make it yours" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/setup/admin")?.body).toEqual({
      code: "AAAA-BBBB-CCCC-DDDD",
      username: "jamie",
      display_name: "Jamie",
      password: "a long enough password",
    });
  });

  test("Visitor without the code: the refusal is shown, what was typed stays, and nothing is signed in or created", async () => {
    const calls = fakeServer({
      "GET /api/me": signedOut(),
      "GET /api/setup/status": { body: { open: true } },
      "POST /api/setup/admin": apiError(403, "forbidden", "That setup code is wrong. Copy it from the server's console output.", "code"),
    });
    await renderApp("/setup");
    await screen.findByLabelText("Setup code");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account and continue" }));
    expect(await screen.findByText("That setup code is wrong. Copy it from the server's console output.")).toBeInTheDocument();
    expect(screen.getByLabelText("Setup code")).toHaveValue("AAAA-BBBB-CCCC-DDDD");
    expect(screen.getByLabelText("Setup code")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("heading", { name: "Create the administrator" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Make it yours" })).toBeNull();
    // Nothing but the one refused attempt was sent: no branding, no settings.
    expect(calls.filter((c) => c.method !== "GET").map((c) => c.path)).toEqual(["/api/setup/admin"]);
  });

  test("too many wrong guesses say try later, and the form can be sent again", async () => {
    fakeServer({
      "GET /api/me": signedOut(),
      "GET /api/setup/status": { body: { open: true } },
      "POST /api/setup/admin": apiError(429, "throttled", "Too many attempts, try again later."),
    });
    await renderApp("/setup");
    await screen.findByLabelText("Setup code");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account and continue" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts, try again later.");
    expect(screen.getByRole("button", { name: "Create account and continue" })).toBeEnabled();
  });

  test("Setup closed: a visitor is sent to sign in", async () => {
    fakeServer({ "GET /api/me": signedOut(), "GET /api/setup/status": apiError(404, "not_found", "Not found.") });
    await renderApp("/setup");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  test("a signed-in user without settings access is sent home, not into the wizard", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") } });
    await renderApp("/setup");
    expect(screen.queryByRole("heading", { name: "Make it yours" })).toBeNull();
    expect(screen.queryByLabelText("Setup code")).toBeNull();
  });
});

describe("ADMIN-1 Name, logo and colour scheme: step 2", () => {
  test("Accent fails on its text: the refusal and the suggested shade are shown, and nothing is saved", async () => {
    const refused: BrandingCheck = {
      ok: false,
      failed: ["on_accent"],
      on_accent_ratio: 1.9,
      as_text_ratio: 3.2,
      suggestion: "#8A5A12",
      colours: hearth,
    };
    const calls = signedInServer({
      "POST /api/admin/branding/check": (body) => ((body as { accent: string }).accent === "#E8B94A" ? { body: refused } : { body: passes }),
    });
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await type("Accent colour (optional)", "#E8B94A");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Text would be hard to read on this colour");
    expect(alert).toHaveTextContent("1.9 : 1");
    expect(alert).toHaveTextContent("at least 4.5 : 1");
    expect(alert).toHaveTextContent("#8A5A12");
    expect(alert).toHaveTextContent("Your scheme hasn't changed.");
    expect(screen.getByRole("button", { name: /Continue to borrowing rules/ })).toBeDisabled();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    // The suggestion can be taken, and then the step can go on.
    await userEvent.click(within(alert).getByRole("button", { name: "Use #8A5A12" }));
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(screen.getByLabelText("Accent colour (optional)")).toHaveValue("#8A5A12");
    expect(screen.getByRole("button", { name: /Continue to borrowing rules/ })).toBeEnabled();
  });

  test("an accent that fails as link text is refused with its own ratio", async () => {
    signedInServer({
      "POST /api/admin/branding/check": {
        body: { ok: false, failed: ["as_text"], on_accent_ratio: 6, as_text_ratio: 2.4, suggestion: "#7A6A20", colours: hearth },
      },
    });
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await type("Accent colour (optional)", "#CCCC66");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("used as text on the page has a contrast of 2.4 : 1");
    expect(alert).not.toHaveTextContent("Button text");
  });

  test("a malformed accent is said at once and never sent for checking", async () => {
    const calls = signedInServer();
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await type("Accent colour (optional)", "yellow");
    expect(await screen.findByText("Enter a colour like #8A5A12, or leave it empty.")).toBeInTheDocument();
    expect(calls.filter((c) => c.path.endsWith("/check") && (c.body as { accent: string }).accent === "yellow")).toHaveLength(0);
    expect(screen.getByRole("button", { name: /Continue to borrowing rules/ })).toBeDisabled();
  });

  test("choosing a scheme asks the server and draws the preview in its colours; continuing saves name, scheme and accent", async () => {
    const calls = signedInServer({
      "POST /api/admin/branding/check": (body) =>
        (body as { scheme: string }).scheme === "inkwell" ? { body: { ...passes, colours: inkwell } } : { body: passes },
      "PUT /api/admin/branding": { body: { ...settings, name: "The Okafor-Hart Family Library", scheme: "inkwell" } },
    });
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await userEvent.clear(screen.getByLabelText("Library name"));
    await type("Library name", "The Okafor-Hart Family Library");
    await userEvent.click(screen.getByRole("radio", { name: /Inkwell/ }));
    expect(screen.getByRole("radio", { name: /Inkwell/ })).toHaveAttribute("aria-checked", "true");
    await waitFor(() => expect(screen.getByTestId("preview").style.getPropertyValue("--primary")).toBe("#2C4A6E"));
    expect(within(screen.getByTestId("preview")).getByText("The Okafor-Hart Family Library")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Continue to borrowing rules/ }));
    expect(await screen.findByRole("heading", { name: "How borrowing works" })).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      name: "The Okafor-Hart Family Library",
      scheme: "inkwell",
      accent: "",
    });
  });

  test("the server's refusal of the name is said under it and the step stays", async () => {
    signedInServer({ "PUT /api/admin/branding": apiError(422, "invalid", "The library needs a name.", "name") });
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await userEvent.clear(screen.getByLabelText("Library name"));
    await userEvent.click(screen.getByRole("button", { name: /Continue to borrowing rules/ }));
    expect(await screen.findByText("The library needs a name.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Make it yours" })).toBeInTheDocument();
  });

  test("a logo the server refuses is said, in its words", async () => {
    signedInServer({ "PUT /api/admin/branding/logo": apiError(422, "invalid", "The logo must be a JPEG, PNG or WebP image of at most 2 MB.", "logo") });
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await userEvent.upload(screen.getByLabelText("Logo file"), new File(["x"], "logo.png", { type: "image/png" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The logo must be a JPEG, PNG or WebP image of at most 2 MB.");
  });

  test("skipping goes on without saving anything", async () => {
    const calls = signedInServer();
    await renderApp("/setup");
    await screen.findByRole("heading", { name: "Make it yours" });
    await userEvent.click(screen.getByRole("button", { name: "Skip" }));
    expect(await screen.findByRole("heading", { name: "How borrowing works" })).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });
});

describe("setup steps 3 and 4", () => {
  test("the time zone starts as the browser's, and the rules are saved", async () => {
    vi.spyOn(Intl.DateTimeFormat.prototype, "resolvedOptions").mockReturnValue({ timeZone: "Africa/Lagos" } as Intl.ResolvedDateTimeFormatOptions);
    const calls = signedInServer({ "PUT /api/admin/settings": { body: { ...settings, time_zone: "Africa/Lagos" } } });
    await renderApp("/setup");
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    expect(await screen.findByLabelText("Time zone")).toHaveValue("Africa/Lagos");
    await userEvent.clear(screen.getByLabelText("Loan period (days)"));
    await type("Loan period (days)", "21");
    await userEvent.click(screen.getByRole("button", { name: /Continue to invitations/ }));
    expect(await screen.findByRole("heading", { name: "Invite people" })).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      time_zone: "Africa/Lagos",
      loan_period_days: 21,
      max_loans: 5,
      max_renewals: 2,
    });
    vi.restoreAllMocks();
  });

  test("a value the server refuses is said under its field, and a non-number is said before sending", async () => {
    const calls = signedInServer({ "PUT /api/admin/settings": apiError(422, "invalid", "The loan period must be 1 to 365 days.", "loan_period_days") });
    await renderApp("/setup");
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    await screen.findByRole("heading", { name: "How borrowing works" });
    await userEvent.clear(screen.getByLabelText("Renewals"));
    await type("Renewals", "lots");
    await userEvent.click(screen.getByRole("button", { name: /Continue to invitations/ }));
    expect(screen.getByText("Enter a whole number (0 for none).")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    await userEvent.clear(screen.getByLabelText("Renewals"));
    await type("Renewals", "2");
    await userEvent.clear(screen.getByLabelText("Loan period (days)"));
    await type("Loan period (days)", "999");
    await userEvent.click(screen.getByRole("button", { name: /Continue to invitations/ }));
    expect(await screen.findByText("The loan period must be 1 to 365 days.")).toBeInTheDocument();
  });

  test("invitations are made one at a time and their codes are listed; finishing leaves the wizard", async () => {
    let n = 0;
    const calls = signedInServer({
      "POST /api/admin/invitations": (body) => ({ status: 201, body: { id: ++n, code: `CODE-000${n}`, role: (body as { role: string }).role, status: "open" } }),
    });
    await renderApp("/setup");
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    await screen.findByRole("heading", { name: "Invite people" });
    await userEvent.click(screen.getByRole("button", { name: "Create invitation" }));
    await userEvent.selectOptions(screen.getByLabelText("Who is it for?"), "librarian");
    await userEvent.click(screen.getByRole("button", { name: "Create invitation" }));
    const list = await screen.findByRole("list", { name: "Invitations you made" });
    await waitFor(() => expect(within(list).getAllByRole("listitem")).toHaveLength(2));
    expect(within(list).getByText("CODE-0001")).toBeInTheDocument();
    expect(within(list).getByText("CODE-0002")).toBeInTheDocument();
    expect(calls.filter((c) => c.path === "/api/admin/invitations").map((c) => c.body)).toEqual([{ role: "patron" }, { role: "librarian" }]);
    await userEvent.click(screen.getByRole("button", { name: "Finish setup" }));
    await waitFor(() => expect(screen.queryByRole("heading", { name: "Invite people" })).toBeNull());
  });

  test("an invitation the server refuses is said, and no code is listed", async () => {
    signedInServer({ "POST /api/admin/invitations": apiError(403, "forbidden", "You can't invite someone with more access than you have.") });
    await renderApp("/setup");
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    await userEvent.click(await screen.findByRole("button", { name: "Skip" }));
    await screen.findByRole("heading", { name: "Invite people" });
    await userEvent.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("more access than you have");
    expect(screen.queryByRole("list", { name: "Invitations you made" })).toBeNull();
  });
});
