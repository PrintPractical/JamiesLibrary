import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { AdminUser, Invitation } from "../../api/admin";
import { apiError, fakeServer, meOf, permissionsOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const user = (id: number, name: string, role: string, over: Partial<AdminUser> = {}): AdminUser => ({
  id,
  username: name.toLowerCase(),
  display_name: name,
  role,
  active: true,
  must_change_password: false,
  permissions: permissionsOf[role as keyof typeof permissionsOf],
  from_preset: { differs: false, added: [], removed: [] },
  ...over,
});

const admin = user(1, "Ada", "administrator");
const maya = user(7, "Maya", "patron");
const ray = user(8, "Ray", "librarian");
const usersPage = (users: AdminUser[]) => ({ body: { users, page: 1, per_page: 100, total: users.length } });

const invitation = (id: number, over: Partial<Invitation> = {}): Invitation => ({
  id,
  code: "ABCD-EFGH-JKMN-PQRS",
  role: "patron",
  creator_id: 1,
  created_at: "2026-11-15T10:00:00Z",
  status: "unused",
  used_by: null,
  used_at: null,
  revoked_at: null,
  ...over,
});

function server(extra: Parameters<typeof fakeServer>[0] = {}, role: keyof typeof permissionsOf = "administrator") {
  return fakeServer({
    "GET /api/me": { body: meOf(role) },
    "GET /api/admin/users?page=1&per_page=25": usersPage([admin, maya, ray]),
    "GET /api/admin/users?page=1&per_page=100": usersPage([admin, maya, ray]),
    "GET /api/admin/invitations?page=1&per_page=25": { body: { invitations: [], page: 1, per_page: 25, total: 0 } },
    ...extra,
  });
}

describe("ADMIN-2 Manage users", () => {
  test("Per-user override: the list and the user's page mark a user who differs from the role's preset", async () => {
    const odd = user(7, "Maya", "patron", {
      permissions: [...permissionsOf.patron, "reviews.remove"],
      from_preset: { differs: true, added: ["reviews.remove"], removed: [] },
    });
    server({ "GET /api/admin/users?page=1&per_page=25": usersPage([admin, odd]), "GET /api/admin/users?page=1&per_page=100": usersPage([admin, odd]) });
    await renderApp("/admin/people");
    const maya = (await screen.findByRole("link", { name: /Maya/ })).closest("li") as HTMLElement;
    expect(within(maya).getByText("Differs from patron preset")).toBeInTheDocument();
    const ada = screen.getByRole("link", { name: /Ada/ }).closest("li") as HTMLElement;
    expect(within(ada).queryByText(/Differs from/)).toBeNull();

    await userEvent.click(screen.getByRole("link", { name: /Maya/ }));
    expect(await screen.findByRole("heading", { name: "Maya" })).toBeInTheDocument();
    expect(screen.getByText("Differs from patron preset")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Remove reviews/ })).toBeChecked();
  });

  test("Per-user override: switching a permission sends it and shows the marker from the answer", async () => {
    const after = user(7, "Maya", "patron", {
      permissions: [...permissionsOf.patron, "reports.view"],
      from_preset: { differs: true, added: ["reports.view"], removed: [] },
    });
    const calls = server({ "PUT /api/admin/users/7/permissions/reports.view": { body: after } });
    await renderApp("/admin/people/7");
    expect(screen.queryByText(/Differs from/)).toBeNull();
    await userEvent.click(await screen.findByRole("switch", { name: /View statistics/ }));
    expect(await screen.findByText("Differs from patron preset")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ on: true });
    expect(screen.getByRole("switch", { name: /View statistics/ })).toBeChecked();
  });

  test("Last admin: the server's refusal is shown and the switch stays as it was", async () => {
    server({
      "PUT /api/admin/users/1/permissions/users.manage": apiError(409, "last_admin", "At least one active user must be able to manage users and permissions."),
    });
    await renderApp("/admin/people/1");
    const sw = await screen.findByRole("switch", { name: /^Manage users/ });
    expect(sw).toBeChecked();
    await userEvent.click(sw);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("At least one active user must be able to manage users and permissions.");
    // Right under the switch that was refused, not at the top of the page.
    expect((sw.closest("li") as HTMLElement).contains(alert)).toBe(true);
    expect(screen.getByRole("switch", { name: /^Manage users/ })).toBeChecked();
  });

  test("Last admin: refusing a deactivation shows the message", async () => {
    server({ "POST /api/admin/users/1/deactivate": apiError(409, "last_admin", "You are the last administrator.") });
    await renderApp("/admin/people/1");
    await userEvent.click(await screen.findByRole("button", { name: "Deactivate" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, deactivate" }));
    const msg = await screen.findByText("You are the last administrator.");
    expect(screen.getByRole("region", { name: "Account" }).contains(msg)).toBe(true);
  });

  test("Deactivate asks first, then marks the user deactivated; reactivate undoes it", async () => {
    const off = { ...maya, active: false };
    const calls = server({
      "POST /api/admin/users/7/deactivate": { body: off },
      "POST /api/admin/users/7/reactivate": { body: maya },
    });
    await renderApp("/admin/people/7");
    await userEvent.click(await screen.findByRole("button", { name: "Deactivate" }));
    expect(calls.some((c) => c.path.endsWith("/deactivate"))).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.path.endsWith("/deactivate"))).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Deactivate" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, deactivate" }));
    expect(await screen.findByText("Deactivated")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reactivate" }));
    await waitFor(() => expect(screen.queryByText("Deactivated")).toBeNull());
  });

  test("Role: only roles the admin holds are offered, and setting one sends it", async () => {
    const calls = server({ "PUT /api/admin/users/7/role": { body: { ...maya, role: "librarian", permissions: permissionsOf.librarian } } });
    await renderApp("/admin/people/7");
    await userEvent.selectOptions(await screen.findByRole("combobox", { name: "Role" }), "librarian");
    await userEvent.click(screen.getByRole("button", { name: "Set role" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ role: "librarian" }));
    expect(await screen.findAllByText("Librarian")).not.toHaveLength(0);
  });

  test("Permissions the admin doesn't hold are disabled, with the reason", async () => {
    // A librarian given manage-users and manage-permissions can still not grant the rest.
    const helper = [...permissionsOf.librarian, "users.manage", "users.permissions"];
    fakeServerWith(helper);
    await renderApp("/admin/people/7");
    const reports = await screen.findByRole("switch", { name: /View statistics/ });
    expect(reports).toBeDisabled();
    expect(screen.getAllByText(/You don't hold this permission/).length).toBeGreaterThan(0);
    expect(screen.getByRole("switch", { name: /Manage the catalog/ })).toBeEnabled();
    const roles = within(screen.getByRole("combobox", { name: "Role" }));
    expect(roles.queryByText(/Administrator/)).toBeNull();
  });
});

/** A signed-in librarian who also holds the two user permissions, so the screens open. */
function fakeServerWith(permissions: string[]) {
  const me = meOf("librarian", { permissions });
  fakeServer({
    "GET /api/me": { body: me },
    "GET /api/admin/users?page=1&per_page=100": usersPage([maya]),
  });
}

describe("ACCESS-7 Administrator resets a password", () => {
  test("Reset: the temporary password is shown once, with a warning, and gone when dismissed", async () => {
    const calls = server({
      "POST /api/admin/users/7/reset-password": { body: { user: { ...maya, must_change_password: true }, temporary_password: "k3Hq9xLm2VbN7wTa" } },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    await renderApp("/admin/people/7");
    await userEvent.click(await screen.findByRole("button", { name: "Reset password" }));
    expect(calls.some((c) => c.path.endsWith("reset-password"))).toBe(false);
    await userEvent.click(screen.getByRole("button", { name: "Yes, reset the password" }));
    expect(await screen.findByText("k3Hq9xLm2VbN7wTa")).toBeInTheDocument();
    expect(screen.getByText(/This won't be shown again/)).toBeInTheDocument();
    expect(screen.getByText("Must change password")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("k3Hq9xLm2VbN7wTa");
    await userEvent.click(screen.getByRole("button", { name: "I've passed it on" }));
    expect(screen.queryByText("k3Hq9xLm2VbN7wTa")).toBeNull();
  });

  test("Reset: a refusal is shown and no password appears", async () => {
    server({ "POST /api/admin/users/7/reset-password": apiError(403, "forbidden", "You can't reset a password for someone with more permissions than you.") });
    await renderApp("/admin/people/7");
    await userEvent.click(await screen.findByRole("button", { name: "Reset password" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, reset the password" }));
    expect(await screen.findByText(/more permissions than you/)).toBeInTheDocument();
    expect(screen.queryByText(/won't be shown again/)).toBeNull();
  });
});

describe("ACCESS-3 Manage invitation codes", () => {
  test("Revoke: an unused code is listed with its state and creator, and revoking marks it revoked", async () => {
    const revoked = invitation(3, { status: "revoked", revoked_at: "2026-11-15T11:00:00Z" });
    let list = [invitation(3), invitation(4, { code: "ZZZZ-ZZZZ-ZZZZ-ZZZZ", status: "used", creator_id: 7 })];
    const calls = server({
      "GET /api/admin/invitations?page=1&per_page=25": () => ({ body: { invitations: list, page: 1, per_page: 25, total: 2 } }),
      "POST /api/admin/invitations/3/revoke": () => {
        list = [revoked, ...list.slice(1)];
        return { body: revoked };
      },
    });
    await renderApp("/admin/people");
    const [first, second] = within(await screen.findByRole("list", { name: "Invitation codes" })).getAllByRole("listitem") as [HTMLElement, HTMLElement];
    expect(first).toHaveTextContent("ABCD-EFGH-JKMN-PQRS");
    expect(first).toHaveTextContent("Unused");
    expect(first).toHaveTextContent("made by You");
    expect(second).toHaveTextContent("Used");
    expect(second).toHaveTextContent("made by Maya");
    expect(within(second).queryByRole("button", { name: /Revoke/ })).toBeNull();
    await userEvent.click(within(first).getByRole("button", { name: "Revoke ABCD-EFGH-JKMN-PQRS" }));
    await waitFor(() => expect(within(screen.getByRole("list", { name: "Invitation codes" })).getAllByRole("listitem")[0]).toHaveTextContent("Revoked"));
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/admin/invitations/3/revoke")).toBe(true);
  });

  test("Revoke: a code the server withholds is listed with no code and no revoke button", async () => {
    server({
      "GET /api/admin/invitations?page=1&per_page=25": { body: { invitations: [invitation(5, { code: null, role: "administrator" })], page: 1, per_page: 25, total: 1 } },
    });
    await renderApp("/admin/people");
    const row = (await screen.findByText(/Code hidden/)).closest("li") as HTMLElement;
    expect(row).toHaveTextContent("Administrator");
    expect(within(row).queryByRole("button")).toBeNull();
  });

  test("Create: a new code for the chosen role is made and listed", async () => {
    let list: Invitation[] = [];
    const calls = server({
      "GET /api/admin/invitations?page=1&per_page=25": () => ({ body: { invitations: list, page: 1, per_page: 25, total: list.length } }),
      "POST /api/admin/invitations": (body) => {
        list = [invitation(9, { role: (body as { role: string }).role })];
        return { status: 201, body: list[0] };
      },
    });
    await renderApp("/admin/people");
    await userEvent.selectOptions(await screen.findByLabelText("Role the new account gets"), "librarian");
    await userEvent.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByText("ABCD-EFGH-JKMN-PQRS")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ role: "librarian" });
  });

  test("No escalation: a role with a permission the creator lacks isn't offered", async () => {
    // A librarian holding only manage-invitations may hand out roles up to librarian.
    server({ "GET /api/me": { body: meOf("librarian", { permissions: [...permissionsOf.librarian, "invitations.manage"] }) } }, "librarian");
    await renderApp("/admin/people");
    const select = await screen.findByLabelText("Role the new account gets");
    const options = within(select).getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["Patron: borrows books", "Librarian: runs the desk and the catalog"]);
    expect(screen.getByText(/aren't offered/)).toBeInTheDocument();
  });

  test("No escalation: a refusal from the server is shown when a role is forced", async () => {
    server({ "POST /api/admin/invitations": apiError(403, "forbidden", "You can't invite someone to a role that has permissions you don't hold.") });
    await renderApp("/admin/people");
    await userEvent.selectOptions(await screen.findByLabelText("Role the new account gets"), "administrator");
    await userEvent.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByText(/permissions you don't hold/)).toBeInTheDocument();
  });
});
