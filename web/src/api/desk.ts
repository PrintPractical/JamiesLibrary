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

export type DeskLoan = {
  id: number;
  copy_number: number;
  copy_code: string;
  book_id: number;
  title: string;
  authors: string[];
  patron_name: string;
  due_date: string;
  days_late: number;
  overdue: boolean;
};

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
