import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { homeFor } from "./landing";
import { apiError, fakeServer, meOf, renderApp } from "../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const links = (nav: HTMLElement) => within(nav).getAllByRole("link").map((a) => a.textContent);
const navs = () => screen.getAllByRole("navigation", { name: "Main" });

describe("UX-2 Separate patron and staff experiences", () => {
  test("Staff navigation: Check out and Check in are one tap away, in the side bar and the tab bar", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    await renderApp("/desk/checkout");
    for (const nav of navs()) {
      expect(within(nav).getByRole("link", { name: "Check out" })).toHaveAttribute("href", "/desk/checkout");
      expect(within(nav).getByRole("link", { name: "Check in" })).toHaveAttribute("href", "/desk/checkin");
    }
    expect(screen.queryByText("Admin")).toBeNull();
    expect(links(navs()[0]!)).toEqual(["Check out", "Check in", "Catalog", "Patrons", "Overdue", "My library"]);
  });

  test("Staff navigation: the Admin group shows only the items the user's permissions allow", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["circ.desk", "settings.manage"] }) } });
    await renderApp("/desk/checkout");
    const side = navs()[0]!;
    expect(within(side).getByText("Admin")).toBeInTheDocument();
    expect(within(side).getByRole("link", { name: "Settings" })).toBeInTheDocument();
    expect(within(side).queryByRole("link", { name: "Statistics" })).toBeNull();
    expect(within(side).queryByRole("link", { name: "People & invites" })).toBeNull();
    expect(within(side).queryByRole("link", { name: "Catalog" })).toBeNull();
  });

  test("Staff navigation: an administrator sees the whole Admin group", async () => {
    fakeServer({ "GET /api/me": { body: meOf("administrator") } });
    await renderApp("/desk/checkout");
    const side = navs()[0]!;
    expect(links(side)).toEqual(expect.arrayContaining(["Settings", "People & invites", "Statistics", "Activity log"]));
  });

  test("Patron navigation: patrons get the patron tabs and no staff items", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") } });
    await renderApp("/");
    for (const nav of navs()) expect(links(nav)).toEqual(["Home", "Browse", "My card", "My books", "Account"]);
    for (const nav of navs()) expect(within(nav).queryByRole("link", { name: /check out|check in|desk|catalog|admin/i })).toBeNull();
  });

  test("The Staff link: one link on the patron side to the first screen, and no other staff item", async () => {
    for (const role of ["librarian", "administrator"] as const) {
      fakeServer({ "GET /api/me": { body: meOf(role) } });
      const { unmount } = await renderApp("/card");
      expect(links(navs()[0]!)).toEqual(["Home", "Browse", "My card", "My books", "Account", "Staff"]);
      expect(links(navs()[1]!)).toEqual(["Home", "Browse", "My card", "My books", "Account"]);
      const staff = screen.getAllByRole("link", { name: "Staff" });
      expect(staff).toHaveLength(2); // the side bar's foot and the phone's top bar
      for (const a of staff) expect(a).toHaveAttribute("href", "/desk/checkout");
      expect(screen.queryByRole("link", { name: /check out|check in|patrons|catalog|more|desk/i })).toBeNull();
      unmount();
    }
  });

  test("The Staff link opens the user's first screen, whatever it is", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["reports.view"] }) } });
    await renderApp("/");
    expect(screen.getAllByRole("link", { name: "Staff" })[0]!).toHaveAttribute("href", "/admin/statistics");
  });

  test.each([
    [["catalog.manage"], "/catalog"],
    [["patrons.lookup"], "/desk/patrons"],
    [["settings.manage"], "/admin/settings"],
  ])("The Staff link of a user with %j goes to %s", async (permissions, to) => {
    fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions }) } });
    await renderApp("/");
    for (const a of screen.getAllByRole("link", { name: "Staff" })) expect(a).toHaveAttribute("href", to);
  });

  test.each([["reviews.remove"], ["cards.reissue"]])(
    "A user whose only staff-area permission is %s is patron-side only: no Staff link, Home after sign-in",
    async (permission) => {
      fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["catalog.browse", permission] }) } });
      const { unmount } = await renderApp("/");
      expect(screen.queryByRole("link", { name: "Staff" })).toBeNull();
      expect(homeFor((p) => p.includes(permission))).toBe("/");
      unmount();
      for (const path of ["/desk", "/desk/checkout"]) {
        const view = await renderApp(path);
        expect(screen.queryByRole("heading", { name: "Check out" })).toBeNull();
        expect(await screen.findByRole("heading", { name: /^Good (morning|afternoon|evening)/ })).toBeInTheDocument();
        view.unmount();
      }
    },
  );

  test("Non-staff: a patron is shown no Staff link", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") } });
    await renderApp("/");
    expect(screen.queryByRole("link", { name: "Staff" })).toBeNull();
  });

  test("The old Desk address: /desk opens the first screen", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: "Check out" })).toBeInTheDocument();
    cleanup();
    fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["catalog.manage"] }) } });
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: /^Catalog/ })).toBeInTheDocument();
  });

  test("Staff have no Desk page: no Desk item in the navigation", async () => {
    fakeServer({ "GET /api/me": { body: meOf("administrator") } });
    await renderApp("/desk/checkout");
    expect(screen.queryByRole("link", { name: /^Desk$/ })).toBeNull();
  });

  test.each([
    [["circ.desk"], "/desk/checkout"],
    [["catalog.manage"], "/catalog"],
    [["patrons.lookup"], "/desk/patrons"],
    [["settings.manage"], "/admin/settings"],
    [["reviews.remove"], "/"],
    [["cards.reissue"], "/"],
    [["circ.desk", "settings.manage"], "/desk/checkout"],
    [["patrons.lookup", "catalog.manage"], "/catalog"],
  ])("After signing in, a user with %j arrives at %s", (permissions, to) => {
    expect(homeFor((wanted) => wanted.some((p) => permissions.includes(p)))).toBe(to);
  });

  test("Patron navigation: a patron who opens a staff address is sent to the patron home", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") } });
    await renderApp("/desk/checkout");
    expect(await screen.findByRole("heading", { name: /^Good (morning|afternoon|evening), Pat$/ })).toBeInTheDocument();
  });

  test("Patron navigation: a patron's items follow their permissions", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron", { permissions: ["catalog.browse"] }) } });
    await renderApp("/");
    expect(links(navs()[0]!)).toEqual(["Home", "Browse", "My card", "Account"]);
  });

  test("a page the user lacks the permission for says so", async () => {
    fakeServer({ "GET /api/me": { body: meOf("librarian") } });
    await renderApp("/admin/settings");
    expect(screen.getByRole("heading", { name: "You don't have access to this page" })).toBeInTheDocument();
  });

  test("names are shown as text, never as markup", async () => {
    const name = '<img src=x onerror="window.pwned=1">';
    fakeServer({ "GET /api/me": { body: meOf("patron", { user: { id: 3, username: "p", display_name: name, role: "patron" } }) } });
    const { container } = await renderApp("/account");
    expect(screen.getAllByText(name).length).toBeGreaterThan(0);
    expect(container.querySelector("img[src=x]")).toBeNull();
  });
});

describe("route guards", () => {
  test.each(["/", "/catalog", "/desk", "/desk/checkout", "/admin/settings", "/account", "/books/3", "/nope"])(
    "a signed-out visitor at %s is sent to sign in",
    async (path) => {
      fakeServer({ "GET /api/me": apiError(401, "unauthorized", "Sign in to continue.") });
      await renderApp(path);
      expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
      expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
    },
  );

  test("with the public catalog on a visitor is sent to the catalog, and still not to staff pages", async () => {
    const publicLibrary = { body: { name: "Maple", scheme: "hearth", logo_url: "", public_catalog: true, colours: {} } };
    fakeServer({ "GET /api/library": publicLibrary, "GET /api/me": apiError(401, "unauthorized", "Sign in to continue.") });
    const { unmount } = await renderApp("/");
    expect(await screen.findByRole("heading", { name: "Browse" })).toBeInTheDocument();
    expect(within(navs()[0]!).getByRole("link", { name: "Sign in" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "My card" })).toBeNull();
    unmount();
    await renderApp("/desk");
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  });

  test("a user who must change their password reaches nothing else until they have", async () => {
    let must = true;
    const calls = fakeServer({
      "GET /api/me": () => ({ body: meOf("librarian", { must_change_password: must }) }),
      "POST /api/me/password": () => {
        must = false;
        return { status: 204 };
      },
    });
    await renderApp("/desk/checkout");
    expect(screen.getByRole("heading", { name: "Choose a new password" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
    await userEvent.type(screen.getByLabelText("Current password"), "temporary-pass-1");
    await userEvent.type(screen.getByLabelText("New password"), "a much better password");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByRole("heading", { name: "Check out" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/me/password")?.body).toEqual({
      current_password: "temporary-pass-1",
      new_password: "a much better password",
    });
  });

  test("the library's colours are applied to the page", async () => {
    fakeServer({
      "GET /api/library": { body: { name: "Maple", scheme: "x", logo_url: "", public_catalog: false, colours: { "--primary": "#112233", "--paper": "javascript:1", "--nope": "#000000" } } },
      "GET /api/me": apiError(401, "unauthorized", "Sign in to continue."),
    });
    await renderApp("/signin");
    const root = document.documentElement.style;
    expect(root.getPropertyValue("--primary")).toBe("#112233");
    expect(root.getPropertyValue("--paper")).toBe("#f7f0e3"); // not a colour: Hearth's stays
    expect(root.getPropertyValue("--nope")).toBe("");
    expect(screen.getByText("Maple")).toBeInTheDocument();
  });

  test("a failed load shows a page error with a retry", async () => {
    fakeServer({ "GET /api/me": { status: 500, body: { error: { code: "internal", message: "Something broke (request abc)." } } } });
    await renderApp("/");
    expect(screen.getByRole("alert")).toHaveTextContent("Something broke");
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
