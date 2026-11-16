import { api } from "./client";

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
