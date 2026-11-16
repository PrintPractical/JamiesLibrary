import type { ReactElement, ReactNode } from "react";
import { Navigate, Outlet, useLocation, useRoutes, type RouteObject } from "react-router";
import { PatronLayout, StaffLayout, StaffMore } from "../components/Layouts";
import type { ShellUser } from "../components/Shell";
import { Account } from "../features/account/Account";
import { ForcedChange } from "../features/account/ForcedChange";
import { Register } from "../features/account/Register";
import { SignIn } from "../features/account/SignIn";
import { Card } from "../features/card/Card";
import { CheckIn } from "../features/desk/CheckIn";
import { Checkout } from "../features/desk/Checkout";
import { BookPage } from "../features/catalog/BookPage";
import { Browse } from "../features/catalog/Browse";
import { Activity } from "../features/admin/Activity";
import { Statistics } from "../features/admin/Statistics";
import { AddBook } from "../features/catalog/AddBook";
import { Overdue } from "../features/patrons/Overdue";
import { PatronPage } from "../features/patrons/PatronPage";
import { Patrons } from "../features/patrons/Patrons";
import { People } from "../features/people/People";
import { UserPage } from "../features/people/UserPage";
import { Settings } from "../features/settings/Settings";
import { Setup } from "../features/setup/Setup";
import { NotFound } from "../features/placeholder/NotFound";
import { Placeholder } from "../features/placeholder/Placeholder";
import { allowed, adminItems, isStaff, patronItems, staffFooterItems, staffItems } from "./navigation";
import { useSession } from "./session";

const page = (title: string): ReactElement => <Placeholder title={title} />;

/** Every route's own check, shown as the page it guards. Display only: the server decides. */
function Allow({ any, children }: { any: readonly string[]; children: ReactNode }) {
  const { me, canAny } = useSession();
  // A visitor only gets this far where the guard let them in: the public catalog.
  if (!me || canAny(any)) return children;
  return (
    <div className="placeholder">
      <h1>You don't have access to this page</h1>
      <p className="muted">Ask an administrator if you think you should.</p>
    </div>
  );
}

const gated = (any: readonly string[], title: string): ReactElement => <Allow any={any}>{page(title)}</Allow>;

// What a visitor may open while the public catalog is on.
const PUBLIC_CATALOG = [/^\/browse$/, /^\/books\/[^/]+$/];

/**
 * Sends a visitor to sign in (or to the public catalog, when it is on), and holds
 * a user who must change their password at that screen until they have.
 */
function Guard() {
  const { me, library } = useSession();
  const { pathname, search } = useLocation();
  if (!me) {
    if (library.public_catalog) {
      if (pathname === "/") return <Navigate to="/browse" replace />;
      if (PUBLIC_CATALOG.some((re) => re.test(pathname))) return <Outlet />;
    }
    return <Navigate to="/signin" replace state={{ from: pathname + search }} />;
  }
  if (me.must_change_password) return <ForcedChange />;
  return <Outlet />;
}

function initials(name: string): string {
  const letters = name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => Array.from(w)[0] ?? "");
  return letters.join("").toUpperCase();
}

function useShellUser(): ShellUser | undefined {
  const { me } = useSession();
  if (!me) return undefined;
  const role = me.user.role;
  return { name: me.user.display_name, initials: initials(me.user.display_name), roleLabel: role.charAt(0).toUpperCase() + role.slice(1) };
}

const visitorItems = [
  { to: "/browse", label: "Browse", icon: "search" as const },
  { to: "/signin", label: "Sign in", icon: "account" as const },
];

function PatronArea() {
  const { me, library, canAny } = useSession();
  const user = useShellUser();
  const items = me ? allowed(patronItems, canAny) : visitorItems;
  return <PatronLayout items={items} libraryName={library.name} logoUrl={library.logo_url || undefined} user={user} />;
}

function More() {
  const { canAny } = useSession();
  // What the staff tab bar has no room for: its first four items stay in it.
  const items = [...allowed(staffItems, canAny).slice(4), ...allowed(adminItems, canAny), ...staffFooterItems];
  return <StaffMore items={items} />;
}

function StaffArea() {
  const { library, canAny } = useSession();
  const user = useShellUser();
  if (!isStaff(canAny)) return <Navigate to="/" replace />;
  const items = allowed(staffItems, canAny);
  const admin = allowed(adminItems, canAny);
  return (
    <StaffLayout
      items={items}
      adminItems={admin}
      footerItems={staffFooterItems}
      libraryName={library.name}
      logoUrl={library.logo_url || undefined}
      user={user}
    />
  );
}

/** The route table of architecture §14. Each page is a placeholder until its feature is built. */
export const routeTable: RouteObject[] = [
  {
    element: <Guard />,
    children: [
      {
        element: <PatronArea />,
        children: [
          { index: true, element: page("Home") },
          { path: "browse", element: <Allow any={["catalog.browse"]}><Browse /></Allow> },
          { path: "card", element: <Card /> },
          { path: "loans", element: gated(["loans.own.view"], "My books") },
          { path: "account", element: <Account /> },
          { path: "books/:id", element: <Allow any={["catalog.browse"]}><BookPage /></Allow> },
          { path: "*", element: <NotFound /> },
        ],
      },
      {
        element: <StaffArea />,
        children: [
          { path: "desk", element: page("Desk") },
          { path: "desk/checkout", element: <Allow any={["circ.desk"]}><Checkout /></Allow> },
          { path: "desk/checkin", element: <Allow any={["circ.desk"]}><CheckIn /></Allow> },
          { path: "desk/patrons", element: <Allow any={["patrons.lookup"]}><Patrons /></Allow> },
          { path: "desk/patrons/:id", element: <Allow any={["patrons.lookup"]}><PatronPage /></Allow> },
          { path: "desk/overdue", element: <Allow any={["circ.desk"]}><Overdue /></Allow> },
          { path: "catalog", element: gated(["catalog.manage"], "Catalog") },
          { path: "catalog/new", element: <Allow any={["catalog.manage"]}><AddBook /></Allow> },
          { path: "admin/settings", element: <Allow any={["settings.manage"]}><Settings /></Allow> },
          {
            path: "admin/people",
            element: (
              <Allow any={["users.manage", "users.permissions", "invitations.manage"]}>
                <People />
              </Allow>
            ),
          },
          { path: "admin/people/:id", element: <Allow any={["users.manage"]}><UserPage /></Allow> },
          { path: "admin/statistics", element: <Allow any={["reports.view"]}><Statistics /></Allow> },
          { path: "admin/activity", element: <Allow any={["activity.view"]}><Activity /></Allow> },
          { path: "more", element: <More /> },
        ],
      },
    ],
  },
  { path: "setup", element: <Setup /> },
  { path: "signin", element: <SignIn /> },
  { path: "register", element: <Register /> },
];

export function AppRoutes() {
  return useRoutes(routeTable);
}
