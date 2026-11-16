import type { NavItem } from "../components/Shell";

/** A nav item and the permissions that show it: any one of them is enough. Display only: the server decides. */
export type GatedItem = NavItem & { any?: readonly string[] };

// What takes a user into the staff area at all. The Desk (staff home) shows for any of these.
const STAFF_AREA = [
  "circ.desk",
  "patrons.lookup",
  "catalog.manage",
  "reviews.remove",
  "cards.reissue",
  "reports.view",
  "activity.view",
  "settings.manage",
  "invitations.manage",
  "users.manage",
  "users.permissions",
] as const;

export const patronItems: GatedItem[] = [
  { to: "/", label: "Home", icon: "home", end: true },
  { to: "/browse", label: "Browse", icon: "search", any: ["catalog.browse"] },
  { to: "/card", label: "My card", icon: "card" },
  { to: "/loans", label: "My books", icon: "books", any: ["loans.own.view"] },
  { to: "/account", label: "Account", icon: "account" },
];

export const staffItems: GatedItem[] = [
  { to: "/desk", label: "Desk", icon: "home", end: true, any: STAFF_AREA },
  { to: "/desk/checkout", label: "Check out", icon: "checkout", any: ["circ.desk"] },
  { to: "/desk/checkin", label: "Check in", icon: "checkin", any: ["circ.desk"] },
  { to: "/catalog", label: "Catalog", icon: "catalog", any: ["catalog.manage"] },
  { to: "/desk/patrons", label: "Patrons", icon: "patrons", any: ["patrons.lookup"] },
  { to: "/desk/overdue", label: "Overdue", icon: "overdue", any: ["circ.desk"] },
];

export const adminItems: GatedItem[] = [
  { to: "/admin/settings", label: "Settings", icon: "settings", any: ["settings.manage"] },
  { to: "/admin/people", label: "People & invites", icon: "people", any: ["users.manage", "users.permissions", "invitations.manage"] },
  { to: "/admin/statistics", label: "Statistics", icon: "stats", any: ["reports.view"] },
];

export const staffFooterItems: NavItem[] = [{ to: "/", label: "My library", icon: "switch", end: true }];

/** Whether a user holding `canAny`'s permissions may see an item. */
export function visible(item: GatedItem, canAny: (permissions: readonly string[]) => boolean): boolean {
  return item.any === undefined || canAny(item.any);
}

/** The items a user may see. */
export function allowed(items: GatedItem[], canAny: (permissions: readonly string[]) => boolean): NavItem[] {
  return items.filter((i) => visible(i, canAny));
}

/** Whether the user has any part of the staff area. */
export const isStaff = (canAny: (permissions: readonly string[]) => boolean) => canAny(STAFF_AREA);
