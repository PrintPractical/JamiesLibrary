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
