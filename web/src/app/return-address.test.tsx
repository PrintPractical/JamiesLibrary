import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { AppRoutes } from "./routes";
import { SessionProvider } from "./session";
import { fakeServer, meOf, signedOut } from "../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

/** Opens /signin the way the guard does, remembering `from`, signs in as a patron, and says what the page became. */
async function signInAfter(from: unknown) {
  let signedIn = false;
  fakeServer({
    "GET /api/me": () => (signedIn ? { body: meOf("patron") } : signedOut()),
    "POST /api/auth/signin": () => {
      signedIn = true;
      return { body: meOf("patron") };
    },
  });
  const view = render(
    <SessionProvider>
      <MemoryRouter initialEntries={[{ pathname: "/signin", state: { from } }]}>
        <AppRoutes />
      </MemoryRouter>
    </SessionProvider>,
  );
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  await userEvent.type(screen.getByLabelText("Username"), "pat");
  await userEvent.type(screen.getByLabelText("Password"), "a long enough password");
  await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Sign in" })).toBeNull());
  return view;
}

describe("ACCESS-5 the page a visitor was sent from is followed only when it is in this app", () => {
  test.each(["//evil.example", "/\\evil.example", "https://evil.example/", "javascript:alert(1)", "evil.example", "/\nx", 7, null])(
    "a return address of %j is ignored: the patron lands on their own home",
    async (from) => {
      await signInAfter(from);
      expect(screen.queryByText("Page not found")).toBeNull();
      expect((await screen.findAllByRole("navigation")).length).toBeGreaterThan(0);
    },
  );

  test("a page of this app is followed", async () => {
    await signInAfter("/account");
    expect(await screen.findByRole("heading", { name: "Account" })).toBeInTheDocument();
  });
});
