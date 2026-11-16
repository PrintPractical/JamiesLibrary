import { api } from "./client";

export type Loan = {
  id: number;
  copy_code: string;
  book_id: number;
  title: string;
  authors: string[];
  patron_id: number;
  patron_name: string;
  checked_out_at: string;
  due_date: string;
  returned_at?: string;
  days_late: number;
  overdue: boolean;
  renewals: number;
};

export type LoanPage = { loans: Loan[]; page: number; per_page: number; total: number };

export type Patron = {
  id: number;
  username: string;
  display_name: string;
  card: string;
  active: boolean;
  out: number;
  overdue: number;
};

export type PatronRecord = { patron: Patron; current: Loan[]; overdue: Loan[] };

export const findPatrons = (q: string, signal?: AbortSignal) =>
  api<{ patrons: Patron[] }>(`/api/patrons?q=${encodeURIComponent(q)}`, { signal });

export const getPatron = (id: string, signal?: AbortSignal) =>
  api<PatronRecord>(`/api/patrons/${encodeURIComponent(id)}`, { signal });

export const getHistory = (id: string, page: number, signal?: AbortSignal) =>
  api<LoanPage>(`/api/patron-loans/${encodeURIComponent(id)}?page=${page}`, { signal });

export const getOverdue = (page: number, signal?: AbortSignal) =>
  api<LoanPage>(`/api/desk/overdue?page=${page}`, { signal });

export const renewLoan = (loanId: number) => api<Loan>(`/api/desk/loans/${loanId}/renew`, { method: "POST" });

export const reissueCard = (userId: number) =>
  api<{ number: string }>(`/api/users/${userId}/card/reissue`, { method: "POST" });
