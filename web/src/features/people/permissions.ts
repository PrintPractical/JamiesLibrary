// The 16 permissions of ROLE-1 and the three presets of ROLE-2, for display and for
// offering only the roles an administrator may hand out. The server decides (security design §4).

export const PERMISSIONS: { id: string; label: string; help: string }[] = [
  { id: "catalog.browse", label: "Browse the catalog", help: "Search and view books and their copies." },
  { id: "loans.own.view", label: "See own loans", help: "See their own loans and history." },
  { id: "loans.own.renew", label: "Renew own loans", help: "Renew their own loans." },
  { id: "reviews.write", label: "Rate and review", help: "Write ratings and reviews." },
  { id: "circ.self_checkout", label: "Self-checkout", help: "Check books out to themselves." },
  { id: "patrons.lookup", label: "Look up patrons", help: "Find patrons and see their loans." },
  { id: "circ.desk", label: "Run the desk", help: "Check out, check in and renew for patrons." },
  { id: "cards.reissue", label: "Re-issue cards", help: "Replace a patron's library card." },
  { id: "catalog.manage", label: "Manage the catalog", help: "Add, change and remove books and copies." },
  { id: "reviews.remove", label: "Remove reviews", help: "Remove other people's reviews." },
  { id: "reports.view", label: "View statistics", help: "See reports and circulation counts." },
  { id: "activity.view", label: "View the activity log", help: "See who changed what." },
  { id: "settings.manage", label: "Manage settings", help: "Change the library's name, colours and rules." },
  { id: "invitations.manage", label: "Manage invitations", help: "Make, see and revoke invitation codes." },
  { id: "users.manage", label: "Manage users", help: "List, deactivate and reactivate users, and reset passwords." },
  { id: "users.permissions", label: "Manage permissions", help: "Change users' roles and switch their permissions." },
];

const PATRON = ["catalog.browse", "loans.own.view", "loans.own.renew", "reviews.write", "circ.self_checkout"];
const LIBRARIAN = [...PATRON, "patrons.lookup", "circ.desk", "cards.reissue", "catalog.manage", "reviews.remove"];

export const ROLES: { id: string; label: string; summary: string; preset: readonly string[] }[] = [
  { id: "patron", label: "Patron", summary: "borrows books", preset: PATRON },
  { id: "librarian", label: "Librarian", summary: "runs the desk and the catalog", preset: LIBRARIAN },
  { id: "administrator", label: "Administrator", summary: "everything", preset: PERMISSIONS.map((p) => p.id) },
];

export const roleLabel = (id: string) => ROLES.find((r) => r.id === id)?.label ?? id;

/** The roles whose whole preset is within what the viewer holds. */
export function grantableRoles(can: (permission: string) => boolean) {
  return ROLES.filter((r) => r.preset.every(can));
}
