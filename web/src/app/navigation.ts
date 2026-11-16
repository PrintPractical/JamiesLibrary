import type { NavItem } from "../components/Shell";

/** A nav item and the permissions that show it: any one of them is enough. Display only: the server decides. */
export type GatedItem = NavItem & { any?: readonly string[] };

export const patronItems: GatedItem[] = [
  { to: "/", label: "Home", icon: "home", end: true },
  { to: "/browse", label: "Browse", icon: "search", any: ["catalog.browse"] },
  { to: "/card", label: "My card", icon: "card" },
  { to: "/loans", label: "My books", icon: "books", any: ["loans.own.view"] },
  { to: "/account", label: "Account", icon: "account" },
];

export const staffItems: GatedItem[] = [
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
  { to: "/admin/activity", label: "Activity log", icon: "log", any: ["activity.view"] },
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

/** The staff screens a user may use, in the order UX-2 gives: staff screens, then admin screens. */
export function staffScreens(canAny: (permissions: readonly string[]) => boolean): NavItem[] {
  return allowed([...staffItems, ...adminItems], canAny);
}

/** Where a staff user starts: the first screen they may use, or undefined when they have none. */
export function firstScreen(canAny: (permissions: readonly string[]) => boolean): string | undefined {
  return staffScreens(canAny)[0]?.to;
}

/** Whether the user has any staff screen. A permission with no screen of its own doesn't count. */
export const isStaff = (canAny: (permissions: readonly string[]) => boolean) => firstScreen(canAny) !== undefined;
