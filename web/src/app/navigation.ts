import type { NavItem } from "../components/Shell";

// Every item is shown for now; the auth work filters these by permission.

export const patronItems: NavItem[] = [
  { to: "/", label: "Home", icon: "home", end: true },
  { to: "/browse", label: "Browse", icon: "search" },
  { to: "/card", label: "My card", icon: "card" },
  { to: "/loans", label: "My books", icon: "books" },
  { to: "/account", label: "Account", icon: "account" },
];

export const staffItems: NavItem[] = [
  { to: "/desk", label: "Desk", icon: "home", end: true },
  { to: "/desk/checkout", label: "Check out", icon: "checkout" },
  { to: "/desk/checkin", label: "Check in", icon: "checkin" },
  { to: "/catalog", label: "Catalog", icon: "catalog" },
  { to: "/desk/patrons", label: "Patrons", icon: "patrons" },
  { to: "/desk/overdue", label: "Overdue", icon: "overdue" },
];

export const adminItems: NavItem[] = [
  { to: "/admin/settings", label: "Settings", icon: "settings" },
  { to: "/admin/people", label: "People & invites", icon: "people" },
  { to: "/admin/statistics", label: "Statistics", icon: "stats" },
];

export const staffFooterItems: NavItem[] = [{ to: "/", label: "My library", icon: "switch", end: true }];
