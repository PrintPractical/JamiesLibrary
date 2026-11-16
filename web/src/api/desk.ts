import { api } from "./client";

export type DeskPatron = {
  id: number;
  display_name: string;
  card: string;
  active: boolean;
  out: number;
  overdue: number;
  limit: number;
};

/** One loan as the desk shows it. */
export type DeskLoan = {
  id: number;
  copy_id: number;
  copy_number: number;
  copy_code: string;
  book_id: number;
  title: string;
  authors: string[];
  location: string;
  patron_id: number;
  patron_name: string;
  due_date: string;
  days_late: number;
  overdue: boolean;
};

export type DeskCopy = { id: number; number: number; code: string; title: string; location: string };
export type DeskBook = { id: number; title: string };

/** What a scan at check-in came to: the copy is back, nothing was out, or a loan must be chosen. */
export type CheckinResult = {
  status: "returned" | "not_out" | "choose";
  message: string;
  loan?: DeskLoan;
  choices?: DeskLoan[];
  copy?: DeskCopy;
  book?: DeskBook;
};

/** `loanId` returns that open loan of the book; `card` narrows the book's open loans to that patron's. */
export const checkin = (code: string, which: { loanId?: number; card?: string } = {}) =>
  api<CheckinResult>("/api/desk/checkin", {
    method: "POST",
    body: { code, ...(which.loanId ? { loan_id: which.loanId } : {}), ...(which.card ? { card: which.card } : {}) },
  });

/** Reads a patron's library card. */
export async function resolvePatron(card: string): Promise<DeskPatron> {
  const got = await api<{ patron: DeskPatron }>("/api/desk/resolve", { method: "POST", body: { code: card, step: "patron" } });
  return got.patron;
}

/** Checks a copy (or, in ISBN mode, a book's ISBN) out to the patron with that card, at once. */
export function checkout(card: string, code: string): Promise<DeskLoan> {
  return api<DeskLoan>("/api/desk/checkout", { method: "POST", body: { patron: card, code } });
}

/** What a patron already has out. Needs the patron-lookup permission. */
export async function currentLoans(patronId: number): Promise<DeskLoan[]> {
  const got = await api<{ current: DeskLoan[] }>(`/api/patrons/${patronId}`);
  return got.current;
}
