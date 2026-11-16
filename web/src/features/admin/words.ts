import type { ActivityEntry } from "../../api/admin";
import { permissionLabel } from "../../app/permissions";

const ACTIONS: Record<string, string> = {
  "setup.completed": "Set up the library",
  "settings.changed": "Changed settings",
  "user.role_changed": "Changed a role",
  "user.permission_changed": "Changed a permission",
  "user.deactivated": "Deactivated an account",
  "user.reactivated": "Reactivated an account",
  "user.password_reset": "Reset a password",
  "invitation.created": "Created an invitation",
  "invitation.revoked": "Revoked an invitation",
  "card.reissued": "Reissued a card",
  "review.removed": "Removed a review",
};

/** A sentence for what was done; an action this app doesn't know is still a sentence, never its code. */
export const actionText = (action: string) => ACTIONS[action] ?? "Made a change";

// The Settings screen's words for each setting, with the unit a number is in.
const SETTINGS: Record<string, { label: string; unit?: string }> = {
  name: { label: "Library name" },
  scheme: { label: "Colour scheme" },
  accent: { label: "Accent colour" },
  logo: { label: "Logo" },
  time_zone: { label: "Time zone" },
  loan_period_days: { label: "Loan period", unit: "days" },
  max_loans: { label: "Most loans" },
  max_renewals: { label: "Most renewals" },
  session_idle_minutes: { label: "Sign-out after idle", unit: "minutes" },
  self_checkout: { label: "Self check-out" },
  public_catalog: { label: "Public catalog" },
  copy_id_mode: { label: "How copies are identified" },
};

const VALUES: Record<string, string> = { isbn: "by ISBN", own: "own barcode", set: "set", none: "none" };

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

function valueText(v: unknown): string {
  if (typeof v === "boolean") return v ? "on" : "off";
  if (typeof v === "number") return String(v);
  if (typeof v === "string") return VALUES[v] ?? v;
  return "";
}

/** "Loan period 14 → 21 days, Most loans 5 → 7" from a settings entry's details. */
function settingsText(details: Record<string, unknown>): string {
  const changes = isObject(details.changes) ? details.changes : { copy_id_mode: details.copy_id_mode };
  const parts: string[] = [];
  for (const [field, change] of Object.entries(changes)) {
    if (!isObject(change)) continue;
    const meta = SETTINGS[field];
    const label = meta?.label ?? "A setting";
    parts.push(`${label} ${valueText(change.from)} → ${valueText(change.to)}${meta?.unit ? ` ${meta.unit}` : ""}`);
  }
  return parts.join(", ");
}

const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** Who or what the entry is about, in words: a person by name, or nothing where the action says it all. */
export function subjectText(e: ActivityEntry): string {
  if (!e.target.startsWith("user:") || e.action === "setup.completed") return "";
  return e.target_name || "an account that no longer exists";
}

/** What changed, as a short phrase; "" where the action and the person say it all. */
export function detailText(e: ActivityEntry): string {
  const d = e.details ?? {};
  switch (e.action) {
    case "settings.changed":
      return settingsText(d);
    case "user.role_changed":
      return typeof d.from === "string" && typeof d.to === "string" ? `${cap(d.from)} → ${cap(d.to)}` : "";
    case "user.permission_changed":
      return typeof d.permission === "string" ? `${permissionLabel(d.permission)}: ${d.on ? "granted" : "removed"}` : "";
    case "invitation.created":
      return typeof d.role === "string" ? `for ${d.role}` : "";
    default:
      return "";
  }
}
