import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp, signedOut } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const type = (label: string, text: string) => userEvent.type(screen.getByLabelText(label), text);

describe("ACCESS-5 Sign in", () => {
  test("Wrong password: the server's message is shown, the form keeps what was typed, nothing is signed in", async () => {
    fakeServer({
      "GET /api/me": signedOut(),
      "POST /api/auth/signin": apiError(401, "unauthorized", "The username or password is wrong."),
    });
    await renderApp("/signin");
    await type("Username", "pat");
    await type("Password", "not the password");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The username or password is wrong.");
    expect(screen.getByLabelText("Username")).toHaveValue("pat");
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeInTheDocument();
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  test("too many attempts and a deactivated account are said in the server's words", async () => {
    let reply = apiError(429, "throttled", "Too many attempts, try again later.");
    fakeServer({ "GET /api/me": signedOut(), "POST /api/auth/signin": () => reply });
    await renderApp("/signin");
    await type("Username", "pat");
    await type("Password", "whatever it is");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts, try again later.");
    reply = apiError(403, "forbidden", "This account is deactivated. Ask a librarian or an administrator.");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("This account is deactivated."));
  });

  test("a network failure is said, and the form can be sent again", async () => {
    fakeServer({ "GET /api/me": signedOut() });
    await renderApp("/signin");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
    await type("Username", "pat");
    await type("Password", "whatever it is");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Can't reach the library");
    expect(screen.getByRole("button", { name: "Sign in" })).toBeEnabled();
  });

  test("signing in sends staff to the desk, and back to where a visitor was heading", async () => {
    let signedIn = false;
    const calls = fakeServer({
      "GET /api/me": () => (signedIn ? { body: meOf("librarian") } : signedOut()),
      "POST /api/auth/signin": () => {
        signedIn = true;
        return { body: meOf("librarian") };
      },
    });
    await renderApp("/desk/patrons");
    await screen.findByLabelText("Username");
    await type("Username", "  librarian ");
    await type("Password", "correct horse battery");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { name: "Patrons" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/auth/signin")?.body).toEqual({
      username: "librarian",
      password: "correct horse battery",
    });
  });

  test("a patron lands on the patron home", async () => {
    fakeServer({
      "GET /api/me": signedOut(),
      "POST /api/auth/signin": { body: meOf("patron") },
    });
    await renderApp("/signin");
    await type("Username", "patron");
    await type("Password", "correct horse battery");
    await userEvent.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("heading", { name: "Home" })).toBeInTheDocument();
  });

  test("signing out ends the session, empties the app's caches and returns to sign in", async () => {
    const deleted: string[] = [];
    vi.stubGlobal("caches", { keys: async () => ["static-1", "covers"], delete: async (k: string) => deleted.push(k) });
    const calls = fakeServer({
      "GET /api/me": { body: meOf("patron") },
      "POST /api/auth/signout": { status: 204 },
    });
    await renderApp("/account");
    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/auth/signout")).toBe(true);
    expect(deleted).toEqual(["static-1", "covers"]);
    expect(screen.queryByRole("navigation", { name: "Main" })).toBeNull();
  });

  test("a sign-out that fails leaves the user signed in and says why", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("patron") },
      "POST /api/auth/signout": apiError(500, "internal", "Something went wrong (request r1)."),
    });
    await renderApp("/account");
    await userEvent.click(screen.getByRole("button", { name: "Sign out" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Something went wrong");
    expect(screen.getByRole("heading", { name: "Account" })).toBeInTheDocument();
  });
});

describe("ACCESS-6 Change own password", () => {
  test("Too short: the minimum is shown up front and the server's refusal appears under the field", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("patron") },
      "POST /api/me/password": apiError(422, "invalid", "A password must be at least 10 characters.", "new_password"),
    });
    await renderApp("/account");
    const field = screen.getByLabelText("New password");
    expect(screen.getByText("At least 10 characters.")).toBeInTheDocument();
    await type("Current password", "the old password");
    await userEvent.type(field, "short");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));
    const message = await screen.findByText("A password must be at least 10 characters.");
    expect(field).toHaveAttribute("aria-invalid", "true");
    expect(field.getAttribute("aria-describedby")).toContain(message.id);
    expect(screen.getByLabelText("Current password")).toHaveValue("the old password");
  });

  test("a wrong current password is shown under that field", async () => {
    fakeServer({
      "GET /api/me": { body: meOf("patron") },
      "POST /api/me/password": apiError(422, "invalid", "The current password is wrong.", "current_password"),
    });
    await renderApp("/account");
    await type("Current password", "not mine");
    await type("New password", "a much better password");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));
    const message = await screen.findByText("The current password is wrong.");
    expect(screen.getByLabelText("Current password").getAttribute("aria-describedby")).toContain(message.id);
  });

  test("a changed password is confirmed and the fields are emptied", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") }, "POST /api/me/password": { status: 204 } });
    await renderApp("/account");
    await type("Current password", "the old password");
    await type("New password", "a much better password");
    await userEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Your password is changed.");
    expect(screen.getByLabelText("New password")).toHaveValue("");
  });
});

describe("ACCESS-2 Registration by invitation code", () => {
  const fill = async () => {
    await type("Invitation code", "ABCD-EFGH-JKMN-PQRS");
    await type("Your name", "Sam Reader");
    await type("Username", "sam");
    await type("Password", "a long enough password");
  };

  test("Invalid or used code: the message is shown under the code, and the form keeps the rest", async () => {
    fakeServer({
      "GET /api/me": signedOut(),
      "POST /api/register": apiError(422, "invalid", "That invitation code isn't valid. Check it, or ask for a new one.", "code"),
    });
    await renderApp("/register");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    const message = await screen.findByText("That invitation code isn't valid. Check it, or ask for a new one.");
    expect(screen.getByLabelText("Invitation code").getAttribute("aria-describedby")).toContain(message.id);
    expect(screen.getByLabelText("Username")).toHaveValue("sam");
    expect(screen.getByRole("heading", { name: "Create your account" })).toBeInTheDocument();
  });

  test("a refusal naming the password or the username is shown under that field", async () => {
    let reply = apiError(422, "invalid", "A password must be at least 10 characters.", "password");
    fakeServer({ "GET /api/me": signedOut(), "POST /api/register": () => reply });
    await renderApp("/register");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    const message = await screen.findByText("A password must be at least 10 characters.");
    expect(screen.getByLabelText("Password").getAttribute("aria-describedby")).toContain(message.id);
    reply = apiError(409, "conflict", "That username is taken.", "username");
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    const taken = await screen.findByText("That username is taken.");
    expect(screen.getByLabelText("Username").getAttribute("aria-describedby")).toContain(taken.id);
    expect(screen.queryByText("A password must be at least 10 characters.")).toBeNull();
  });

  test("too many attempts is shown for the form as a whole", async () => {
    fakeServer({
      "GET /api/me": signedOut(),
      "POST /api/register": apiError(429, "throttled", "Too many attempts, try again later."),
    });
    await renderApp("/register");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Too many attempts, try again later.");
  });

  test("a good code creates the account, signs it in and sends it home", async () => {
    const calls = fakeServer({
      "GET /api/me": signedOut(),
      "POST /api/register": { status: 201, body: meOf("patron") },
    });
    await renderApp("/register");
    await fill();
    await userEvent.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByRole("heading", { name: "Home" })).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/register")?.body).toEqual({
      code: "ABCD-EFGH-JKMN-PQRS",
      display_name: "Sam Reader",
      username: "sam",
      password: "a long enough password",
    });
  });
});
