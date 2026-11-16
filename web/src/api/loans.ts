import { api } from "./client";

/** One loan as the server sends it: due_date is a calendar date (YYYY-MM-DD) in the library's zone. */
export type Loan = {
  id: number;
  copy_number: number;
  copy_code: string;
  book_id: number;
  title: string;
  /** Empty or missing for a book that has since been removed. */
  authors?: string[] | null;
  checked_out_at: string;
  due_date: string;
  returned_at?: string;
  days_late: number;
  overdue: boolean;
  renewals: number;
};

export type LoanPage = { loans: Loan[]; page: number; per_page: number; total: number };

export const myLoans = (signal?: AbortSignal) => api<{ loans: Loan[] }>("/api/me/loans", { signal });

export const myHistory = (page: number, signal?: AbortSignal) =>
  api<LoanPage>(`/api/me/loans/history?page=${page}`, { signal });

export const renew = (id: number) => api<Loan>(`/api/me/loans/${id}/renew`, { method: "POST" });

/** Self-checkout: `code` is a copy's code, or a book's ISBN where the library goes by ISBN. */
export const selfCheckout = (code: string) => api<Loan>("/api/me/checkout", { method: "POST", body: { code } });
