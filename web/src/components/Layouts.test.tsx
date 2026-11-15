import { render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { adminItems, patronItems, staffFooterItems, staffItems } from "../app/navigation";
import { AppRoutes } from "../app/routes";
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

const labels = (nav: HTMLElement) => within(nav).getAllByRole("link").map((a) => a.textContent);

test("PatronLayout shows the same items in the same order in the side bar and the tab bar", () => {
  renderAt("/", <PatronLayout items={patronItems} />);
  const [side, tabs] = screen.getAllByRole("navigation", { name: "Main" });
  const want = ["Home", "Browse", "My card", "My books", "Account"];
  expect(labels(side!)).toEqual(want);
  expect(labels(tabs!)).toEqual(want);
  expect(screen.getByText("page body")).toBeInTheDocument();
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
  renderAt("/desk", <StaffLayout items={staffItems} adminItems={adminItems} footerItems={staffFooterItems} />);
  const tabs = screen.getAllByRole("navigation", { name: "Main" })[1]!;
  expect(labels(tabs)).toEqual(["Desk", "Check out", "Check in", "Catalog", "More"]);
});

test("the /more page lists what the tab bar left out", () => {
  render(
    <MemoryRouter initialEntries={["/more"]}>
      <AppRoutes />
    </MemoryRouter>,
  );
  const more = screen.getByRole("navigation", { name: "More" });
  expect(labels(more)).toEqual(["Patrons", "Overdue", "Settings", "People & invites", "Statistics", "My library"]);
});

test.each([
  ["/", "Home"],
  ["/card", "My card"],
  ["/books/7", "Book"],
  ["/desk/checkout", "Check out"],
  ["/desk/checkin", "Check in"],
  ["/desk/patrons", "Patrons"],
  ["/desk/overdue", "Overdue"],
  ["/catalog/new", "Add a book"],
  ["/admin/settings", "Settings"],
  ["/setup", "Set up the library"],
  ["/signin", "Sign in"],
  ["/register", "Create your account"],
])("the route %s is a placeholder page", (path, title) => {
  render(
    <MemoryRouter initialEntries={[path]}>
      <AppRoutes />
    </MemoryRouter>,
  );
  expect(screen.getByRole("heading", { level: 1, name: title })).toBeInTheDocument();
});

test("an address nobody built shows a page inside the content with a way back", () => {
  render(
    <MemoryRouter initialEntries={["/nope"]}>
      <AppRoutes />
    </MemoryRouter>,
  );
  expect(screen.getByRole("heading", { name: "Page not found" })).toBeInTheDocument();
  expect(screen.getByText("There's nothing at this address.")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Go to your library" })).toHaveAttribute("href", "/");
  expect(screen.getByRole("main")).toHaveClass("content");
});

test("the More tab is current on pages reached through it, and not on the others", () => {
  const { unmount } = render(
    <MemoryRouter initialEntries={["/desk/patrons"]}>
      <AppRoutes />
    </MemoryRouter>,
  );
  const tabs = () => screen.getAllByRole("navigation", { name: "Main" })[1]!;
  expect(within(tabs()).getByRole("link", { name: "More" })).toHaveAttribute("aria-current", "page");
  unmount();
  render(
    <MemoryRouter initialEntries={["/desk/checkout"]}>
      <AppRoutes />
    </MemoryRouter>,
  );
  expect(within(tabs()).getByRole("link", { name: "More" })).not.toHaveAttribute("aria-current");
  expect(within(tabs()).getByRole("link", { name: "Check out" })).toHaveAttribute("aria-current", "page");
});

test("/more on a wide window goes to the desk instead of showing an empty page", () => {
  const original = window.matchMedia;
  window.matchMedia = ((q: string) => ({
    matches: true,
    media: q,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
  try {
    render(
      <MemoryRouter initialEntries={["/more"]}>
        <AppRoutes />
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { name: "Desk" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "More" })).toBeNull();
  } finally {
    window.matchMedia = original;
  }
});
