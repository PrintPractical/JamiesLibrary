import { api } from "./client";

export type Me = {
  user: { id: number; username: string; display_name: string; role: string };
  permissions: string[];
  must_change_password: boolean;
  /** The library's rules a signed-in user needs: the zone due dates are in, the renewal limit, self-checkout. */
  settings: { time_zone: string; loan_period_days: number; max_renewals: number; self_checkout: boolean };
};

/** What anyone may see: the sign-in page draws itself from it. */
export type Library = {
  name: string;
  scheme: string;
  logo_url: string;
  public_catalog: boolean;
  /** CSS custom properties ("--primary") to colour values. */
  colours: Record<string, string>;
};

export const getLibrary = (signal?: AbortSignal) => api<Library>("/api/library", { signal });

export const getMe = (signal?: AbortSignal) => api<Me>("/api/me", { signal });

export const signIn = (username: string, password: string) =>
  api<Me>("/api/auth/signin", { method: "POST", body: { username, password } });

export type Registration = { code: string; username: string; display_name: string; password: string };

export const register = (body: Registration) => api<Me>("/api/register", { method: "POST", body });

export const signOut = () => api("/api/auth/signout", { method: "POST" });

export const changePassword = (current_password: string, new_password: string) =>
  api("/api/me/password", { method: "POST", body: { current_password, new_password } });
