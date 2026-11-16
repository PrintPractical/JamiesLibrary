import { screen } from "@testing-library/react";
import type { RouteObject } from "react-router";
import { isLocalPath } from "../features/account/SignIn";
import { apiError, fakeServer, library, renderApp, signedOut } from "../testing";
import { routeTable } from "./routes";

/** Every path the router's table can match, with parameters filled in and "*" as a path nothing matches. */
function paths(routes: RouteObject[], prefix = ""): string[] {
  return routes.flatMap((r) => {
    const here = r.index ? prefix : r.path ? `${prefix}/${r.path}`.replace(/\/+/g, "/") : prefix;
    const own = r.children ? [] : [here];
    return [...own, ...paths(r.children ?? [], here)];
  });
}

const allPaths = paths(routeTable).map((p) => (p === "" ? "/" : p.replace(/:\w+/g, "7").replace(/\*$/, "no-such-page")));

// What a signed-out visitor's browser may ask: who am I, the library's look, and whether setup is open.
const OPEN_TO_ANYONE = ["/api/me", "/api/library", "/api/setup/status"];
const SIGN_IN_FREE = ["/signin", "/register", "/setup"];

const freshInstall = (publicCatalog: boolean, more: Parameters<typeof fakeServer>[0] = {}) =>
  fakeServer({
    ...more,
    "GET /api/me": signedOut(),
    "GET /api/library": { body: { ...library, public_catalog: publicCatalog } },
    "GET /api/setup/status": { body: { open: true } },
  });

/** Renders `path`, checks it ended at sign in, and clears the page for the next one. */
const signInFor = async (path: string) => {
  const { unmount } = await renderApp(path);
  await expectSignIn();
  unmount();
};

const expectSignIn = async () => {
  expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
  expect(screen.queryByRole("navigation")).toBeNull();
  expect(screen.queryByText("This page isn't built yet.")).toBeNull();
};

describe("ACCESS-1 Anonymous visitor", () => {
  test("the walk covers the router's whole table", () => {
    expect(allPaths).toEqual(expect.arrayContaining(["/", "/catalog", "/desk/patrons/7", "/admin/settings", "/signin", "/setup", "/more"]));
    expect(allPaths.length).toBeGreaterThan(20);
  });

  test.each(allPaths.filter((p) => !SIGN_IN_FREE.includes(p)))(
    "a fresh installation sends %s to sign in, with no shell and no catalog request",
    async (path) => {
      const calls = freshInstall(false);
      await renderApp(path);
      await expectSignIn();
      for (const c of calls) {
        expect(c.method).toBe("GET");
        expect(OPEN_TO_ANYONE).toContain(c.path);
      }
    },
  );

  test("/signin and /register show their own forms, and ask for nothing private", async () => {
    const calls = freshInstall(false);
    (await renderApp("/register")).unmount();
    await renderApp("/signin");
    expect(calls.every((c) => OPEN_TO_ANYONE.includes(c.path))).toBe(true);
  });

  test("/setup shows step 1 while setup is open, and goes to sign in once it is closed", async () => {
    freshInstall(false);
    const open = await renderApp("/setup");
    expect(await screen.findByLabelText("Setup code")).toBeInTheDocument();
    open.unmount();
    fakeServer({
      "GET /api/me": signedOut(),
      "GET /api/setup/status": apiError(404, "not_found", "Not found."),
    });
    await renderApp("/setup");
    await expectSignIn();
  });

  test("the page asked for is kept for after sign-in, and only a path of this app is followed", () => {
    expect(isLocalPath("/desk/patrons?q=a")).toBe(true);
    for (const bad of ["//evil.example", "/\\evil.example", "https://evil.example", "evil.example", "/a\nb", "", undefined, 3]) {
      expect(isLocalPath(bad)).toBe(false);
    }
  });
});

describe("ACCESS-4 Optional public catalog", () => {
  test("Off by default: the catalog and a book go to sign in", async () => {
    freshInstall(false);
    for (const path of ["/browse", "/books/7"]) {
      await signInFor(path);
    }
  });

  test("Enabled: a visitor sees the catalog pages, asking only /api/public/, and staff routes still go to sign in", async () => {
    const calls = freshInstall(true, {
      "GET /api/public/books": { body: { books: [], total: 0, page: 1, page_size: 50 } },
      "GET /api/public/books/7": {
        body: { id: 7, title: "Piranesi", authors: [], isbn: "", description: "", cover_url: "", available_copies: 1, total_copies: 1, rating_average: 0, rating_count: 0 },
      },
    });
    const browse = await renderApp("/browse");
    expect(await screen.findByRole("heading", { name: "Browse" })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Nothing found" })).toBeInTheDocument();
    browse.unmount();
    const book = await renderApp("/books/7");
    expect(await screen.findByRole("heading", { name: "Piranesi" })).toBeInTheDocument();
    book.unmount();
    expect(calls.every((c) => OPEN_TO_ANYONE.includes(c.path) || c.path.startsWith("/api/public/"))).toBe(true);

    for (const path of allPaths.filter((p) => !SIGN_IN_FREE.includes(p) && p !== "/browse" && p !== "/books/7" && p !== "/")) {
      await signInFor(path);
    }
    expect(calls.every((c) => OPEN_TO_ANYONE.includes(c.path) || c.path.startsWith("/api/public/"))).toBe(true);
  });
});
