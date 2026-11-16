/** What a person calls each permission (ROLE-1), by the key the server uses. Keys are never shown. */
export const PERMISSION_LABELS: Record<string, string> = {
  "catalog.browse": "Browse the catalog",
  "loans.own.view": "See own loans and card",
  "loans.own.renew": "Renew own loans",
  "reviews.write": "Rate and review books",
  "circ.self_checkout": "Check out books themselves",
  "patrons.lookup": "Look up patrons",
  "circ.desk": "Check out, check in and renew for patrons",
  "cards.reissue": "Re-issue library cards",
  "catalog.manage": "Manage the catalog",
  "reviews.remove": "Remove reviews",
  "reports.view": "See statistics",
  "activity.view": "See the activity log",
  "settings.manage": "Manage settings",
  "invitations.manage": "Manage invitations",
  "users.manage": "Manage users",
  "users.permissions": "Manage users' permissions",
};

/** A permission's label; one this app doesn't know gets a plain phrase, not its key. */
export const permissionLabel = (key: string) => PERMISSION_LABELS[key] ?? "a permission";
