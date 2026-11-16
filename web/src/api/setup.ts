import { api } from "./client";
import type { Library, Me } from "./auth";

/** Whether first-time setup is still open: the server answers 404 once an administrator exists. */
export const setupStatus = (signal?: AbortSignal) => api<{ open: boolean }>("/api/setup/status", { signal });

export type NewAdmin = { code: string; username: string; display_name: string; password: string };

export const createAdmin = (body: NewAdmin) => api<Me>("/api/setup/admin", { method: "POST", body });

export type Scheme = { id: string; name: string; description: string; colours: Record<string, string> };

/** What an administrator sees of the settings (GET /api/admin/settings). */
export type AdminSettings = Library & {
  accent: string;
  schemes: Scheme[];
  time_zone: string;
  loan_period_days: number;
  max_loans: number;
  max_renewals: number;
};

export const getAdminSettings = (signal?: AbortSignal) => api<AdminSettings>("/api/admin/settings", { signal });

export type Rules = { time_zone: string; loan_period_days: number; max_loans: number; max_renewals: number };

export const putRules = (body: Rules) => api<AdminSettings>("/api/admin/settings", { method: "PUT", body });

export type Branding = { name: string; scheme: string; accent: string };

export const putBranding = (body: Branding) => api<AdminSettings>("/api/admin/branding", { method: "PUT", body });

/** The server's verdict on a scheme and accent, and the colours to draw them with. */
export type BrandingCheck = {
  ok: boolean;
  failed: ("on_accent" | "as_text")[];
  on_accent_ratio: number;
  as_text_ratio: number;
  suggestion: string;
  colours: Record<string, string>;
};

export const checkBranding = (scheme: string, accent: string, signal?: AbortSignal) =>
  api<BrandingCheck>("/api/admin/branding/check", { method: "POST", body: { scheme, accent }, signal });

export const putLogo = (file: File) => {
  const form = new FormData();
  form.append("logo", file);
  return api<AdminSettings>("/api/admin/branding/logo", { method: "PUT", body: form });
};

export const deleteLogo = () => api<AdminSettings>("/api/admin/branding/logo", { method: "DELETE" });

export type Invitation = { id: number; code: string | null; role: string; status: string };

export const createInvitation = (role: string) =>
  api<Invitation>("/api/admin/invitations", { method: "POST", body: { role } });
