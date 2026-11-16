import { api } from "./client";

export type FromPreset = { differs: boolean; added: string[]; removed: string[] };

export type AdminUser = {
  id: number;
  username: string;
  display_name: string;
  role: string;
  active: boolean;
  must_change_password: boolean;
  permissions: string[];
  from_preset: FromPreset;
};

export type UserPage = { users: AdminUser[]; page: number; per_page: number; total: number };

const USER_PAGE = 100;

export const listUsers = (page: number, perPage = 25, signal?: AbortSignal) =>
  api<UserPage>(`/api/admin/users?page=${page}&per_page=${perPage}`, { signal });

/** One user, found by paging through the list: the server has no single-user route. */
export async function getUser(id: number, signal?: AbortSignal): Promise<AdminUser | undefined> {
  for (let page = 1; ; page++) {
    const p = await listUsers(page, USER_PAGE, signal);
    const found = p.users.find((u) => u.id === id);
    if (found) return found;
    if (page * p.per_page >= p.total) return undefined;
  }
}

const userPath = (id: number) => `/api/admin/users/${id}`;

export const changeRole = (id: number, role: string) =>
  api<AdminUser>(`${userPath(id)}/role`, { method: "PUT", body: { role } });

export const setPermission = (id: number, permission: string, on: boolean) =>
  api<AdminUser>(`${userPath(id)}/permissions/${encodeURIComponent(permission)}`, { method: "PUT", body: { on } });

export const deactivateUser = (id: number) => api<AdminUser>(`${userPath(id)}/deactivate`, { method: "POST" });

export const reactivateUser = (id: number) => api<AdminUser>(`${userPath(id)}/reactivate`, { method: "POST" });

export const resetPassword = (id: number) =>
  api<{ user: AdminUser; temporary_password: string }>(`${userPath(id)}/reset-password`, { method: "POST" });

export type Invitation = {
  id: number;
  /** null when the viewer doesn't hold every permission of the invitation's role. */
  code: string | null;
  role: string;
  creator_id: number;
  created_at: string;
  status: string;
  used_by: number | null;
  used_at: string | null;
  revoked_at: string | null;
};

export type InvitationPage = { invitations: Invitation[]; page: number; per_page: number; total: number };

export const listInvitations = (page: number, signal?: AbortSignal) =>
  api<InvitationPage>(`/api/admin/invitations?page=${page}&per_page=25`, { signal });

export const createInvitation = (role: string) =>
  api<Invitation>("/api/admin/invitations", { method: "POST", body: { role } });

export const revokeInvitation = (id: number) =>
  api<Invitation>(`/api/admin/invitations/${id}/revoke`, { method: "POST" });

export type Statistics = { books: number; copies: number; patrons: number; current_loans: number; overdue_loans: number };

export type Circulation = { from: string; to: string; checkouts: number; returns: number };

export type ActivityEntry = {
  id: number;
  at: string;
  /** `id` is null for the server console. */
  actor: { id: number | null; display_name: string };
  action: string;
  target: string;
  /** The person a `user:N` target is, by display name; empty for any other target. */
  target_name: string;
  details: Record<string, unknown> | null;
};

export type ActivityPage = { entries: ActivityEntry[]; page: number; per_page: number; total: number };

export const getStatistics = (signal?: AbortSignal) => api<Statistics>("/api/admin/reports/statistics", { signal });

export const getCirculation = (from: string, to: string, signal?: AbortSignal) =>
  api<Circulation>(`/api/admin/reports/circulation?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`, { signal });

export const getActivity = (page: number, signal?: AbortSignal) => api<ActivityPage>(`/api/admin/activity?page=${page}`, { signal });
