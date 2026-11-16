import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { adminItems, patronItems, staffFooterItems, staffItems } from "../app/navigation";
import { fakeServer, meOf, renderApp } from "../testing";
import { PatronLayout, StaffLayout } from "./Layouts";

function renderAt(path: string, ui: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route element={ui}>
          <Route path="*" element={<p>page body</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

// These render the whole app as an administrator, who sees every item.
beforeEach(() => {
  fakeServer({ "GET /api/me": { body: meOf("administrator") } });
});
afterEach(() => {
  vi.unstubAllGlobals();
});

const labels = (nav: HTMLElement) => within(nav).getAllByRole("link").map((a) => a.textContent);

test("PatronLayout shows the same items in the same order in the side bar and the tab bar", () => {
  renderAt("/", <PatronLayout items={patronItems} />);
  const [side, tabs] = screen.getAllByRole("navigation", { name: "Main" });
  const want = ["Home", "Browse", "My card", "My books", "Account"];
  expect(labels(side!)).toEqual(want);
  expect(labels(tabs!)).toEqual(want);
  expect(screen.getByText("page body")).toBeInTheDocument();
});

test("PatronLayout's staffLink is one link in the side bar and the top bar, and adds no tab or More", () => {
  renderAt("/", <PatronLayout items={patronItems} staffLink={{ to: "/desk/checkout", label: "Staff", icon: "switch" }} />);
  const [side, tabs] = screen.getAllByRole("navigation", { name: "Main" });
  expect(labels(side!)).toEqual(["Home", "Browse", "My card", "My books", "Account", "Staff"]);
  expect(labels(tabs!)).toEqual(["Home", "Browse", "My card", "My books", "Account"]);
  expect(screen.getAllByRole("link", { name: "Staff" })).toHaveLength(2);
  expect(screen.queryByRole("link", { name: "More" })).toBeNull();
});

test("PatronLayout without a staffLink shows no Staff link", () => {
  renderAt("/", <PatronLayout items={patronItems} />);
  expect(screen.queryByRole("link", { name: "Staff" })).toBeNull();
});

test("PatronLayout marks the current page and takes its items as data", () => {
  renderAt("/card", <PatronLayout items={patronItems.filter((i) => i.label !== "Browse")} />);
  const [side, tabs] = screen.getAllByRole("navigation", { name: "Main" });
  expect(labels(side!)).not.toContain("Browse");
  expect(within(side!).getByRole("link", { name: "My card" })).toHaveAttribute("aria-current", "page");
  expect(within(tabs!).getByRole("link", { name: "My card" })).toHaveAttribute("aria-current", "page");
  expect(within(side!).getByRole("link", { name: "Home" })).not.toHaveAttribute("aria-current");
});

test("StaffLayout puts an Admin group in the side bar and leaves it out when there are no admin items", () => {
  const { unmount } = renderAt(
    "/desk/checkout",
    <StaffLayout items={staffItems} adminItems={adminItems} footerItems={staffFooterItems} />,
  );
  const [side] = screen.getAllByRole("navigation", { name: "Main" });
  expect(labels(side!)).toEqual([
    ...staffItems.map((i) => i.label),
    ...adminItems.map((i) => i.label),
    "My library",
  ]);
  expect(within(side!).getByText("Admin")).toBeInTheDocument();
  expect(within(side!).getByRole("link", { name: "Check out" })).toHaveAttribute("aria-current", "page");
  unmount();

  renderAt("/", <StaffLayout items={staffItems} />);
  expect(screen.queryByText("Admin")).not.toBeInTheDocument();
});

test("StaffLayout's tab bar keeps four items and a More tab for the rest", () => {
  renderAt("/desk/checkout", <StaffLayout items={staffItems} adminItems={adminItems} footerItems={staffFooterItems} />);
  const tabs = screen.getAllByRole("navigation", { name: "Main" })[1]!;
  expect(labels(tabs)).toEqual(["Check out", "Check in", "Catalog", "Patrons", "More"]);
});

test("StaffLayout's tab bar counts admin screens after the staff ones, so Settings alone is a tab", () => {
  const settings = adminItems.filter((i) => i.label === "Settings");
  renderAt("/admin/settings", <StaffLayout items={[]} adminItems={settings} footerItems={staffFooterItems} />);
  const tabs = screen.getAllByRole("navigation", { name: "Main" })[1]!;
  expect(labels(tabs)).toEqual(["Settings", "More"]);
});

test("a librarian's phone tabs are Check out, Check in, Catalog, Patrons, with Overdue under More", async () => {
  fakeServer({ "GET /api/me": { body: meOf("librarian") } });
  await renderApp("/desk/checkout");
  expect(labels(screen.getAllByRole("navigation", { name: "Main" })[1]!)).toEqual(["Check out", "Check in", "Catalog", "Patrons", "More"]);
  cleanup();
  await renderApp("/more");
  expect(labels(screen.getByRole("navigation", { name: "More" }))).toEqual(["Overdue", "My library"]);
});

test("a settings-only user's phone tab bar has a Settings tab", async () => {
  fakeServer({ "GET /api/me": { body: meOf("librarian", { permissions: ["settings.manage"] }) } });
  await renderApp("/desk");
  expect(labels(screen.getAllByRole("navigation", { name: "Main" })[1]!)).toEqual(["Settings", "More"]);
});

test("the /more page lists what the tab bar left out", async () => {
  await renderApp("/more");
  const more = screen.getByRole("navigation", { name: "More" });
  expect(labels(more)).toEqual(["Overdue", "Settings", "People & invites", "Statistics", "Activity log", "My library"]);
});

test.each([
  ["/desk/checkout", "Check out"],
  ["/desk/checkin", "Check in"],
])("the route %s is a placeholder page", async (path, title) => {
  await renderApp(path);
  expect(screen.getByRole("heading", { level: 1, name: title })).toBeInTheDocument();
});

test("an address nobody built shows a page inside the content with a way back", async () => {
  await renderApp("/nope");
  expect(screen.getByRole("heading", { name: "Page not found" })).toBeInTheDocument();
  expect(screen.getByText("There's nothing at this address.")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Go to your library" })).toHaveAttribute("href", "/");
  expect(screen.getByRole("main")).toHaveClass("content");
});

test("the More tab is current on pages reached through it, and not on the others", async () => {
  const { unmount } = await renderApp("/desk/overdue");
  const tabs = () => screen.getAllByRole("navigation", { name: "Main" })[1]!;
  expect(within(tabs()).getByRole("link", { name: "More" })).toHaveAttribute("aria-current", "page");
  unmount();
  await renderApp("/desk/checkout");
  expect(within(tabs()).getByRole("link", { name: "More" })).not.toHaveAttribute("aria-current");
  expect(within(tabs()).getByRole("link", { name: "Check out" })).toHaveAttribute("aria-current", "page");
});

test("/more on a wide window goes to the first screen instead of showing an empty page", async () => {
  const original = window.matchMedia;
  window.matchMedia = ((q: string) => ({
    matches: true,
    media: q,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
  try {
    await renderApp("/more");
    expect(await screen.findByRole("heading", { name: "Check out" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "More" })).toBeNull();
  } finally {
    window.matchMedia = original;
  }
});

test("an unknown address says so inside the layout and links home", async () => {
  await renderApp("/nope");
  expect(screen.getByRole("heading", { level: 1, name: "Page not found" })).toBeInTheDocument();
  expect(screen.getByText("There's nothing at this address.")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Go to your library" })).toHaveAttribute("href", "/");
  expect(screen.getAllByRole("navigation", { name: "Main" })).toHaveLength(2);
  expect(screen.getByRole("main")).toContainElement(screen.getByRole("heading", { name: "Page not found" }));
});
