// Helpers for tests that render the app against a pretend server.
import { render, waitFor, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import type { Library, Me } from "./api/auth";
import { AppRoutes } from "./app/routes";
import { SessionProvider } from "./app/session";

export const library: Library = { name: "Maple Street Library", scheme: "hearth", logo_url: "", public_catalog: false, colours: {} };

const PATRON = ["catalog.browse", "loans.own.view", "loans.own.renew", "reviews.write", "circ.self_checkout"];
const LIBRARIAN = [...PATRON, "patrons.lookup", "circ.desk", "cards.reissue", "catalog.manage", "reviews.remove"];
const ADMIN = [
  ...LIBRARIAN,
  "reports.view",
  "activity.view",
  "settings.manage",
  "invitations.manage",
  "users.manage",
  "users.permissions",
];

export const permissionsOf = { patron: PATRON, librarian: LIBRARIAN, administrator: ADMIN };

export function meOf(role: keyof typeof permissionsOf, over: Partial<Me> = {}): Me {
  return {
    user: { id: 1, username: role, display_name: `Pat ${role}`, role },
    permissions: permissionsOf[role],
    must_change_password: false,
    ...over,
  };
}

export type Reply = { status?: number; body?: unknown };
export type Handler = (body: unknown) => Reply;
export type Calls = { method: string; path: string; body: unknown }[];

export const apiError = (status: number, code: string, message: string, field?: string): Reply => ({
  status,
  body: { error: { code, message, ...(field ? { field } : {}) } },
});

/**
 * Stands in for fetch. `handlers` is keyed "METHOD /path"; /api/library is answered unless given.
 * An unexpected call fails the test by answering 500 and being recorded.
 */
export function fakeServer(handlers: Record<string, Handler | Reply>): Calls {
  const calls: Calls = [];
  const all: Record<string, Handler | Reply> = { "GET /api/library": { body: library }, ...handlers };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const body = typeof init?.body === "string" ? JSON.parse(init.body) : undefined;
      calls.push({ method, path, body });
      const h = all[`${method} ${path}`];
      const reply = typeof h === "function" ? h(body) : h;
      if (!reply) return new Response(JSON.stringify({ error: { code: "internal", message: "unexpected call" } }), { status: 500 });
      const status = reply.status ?? 200;
      return new Response(status === 204 ? null : JSON.stringify(reply.body ?? {}), { status });
    }),
  );
  return calls;
}

export const signedOut = (): Reply => apiError(401, "unauthorized", "Sign in to continue.");

/** Renders the whole app at `path` and waits for the session to load. */
export async function renderApp(path: string) {
  const result = render(
    <SessionProvider>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </SessionProvider>,
  );
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  return result;
}
