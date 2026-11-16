import { api } from "./client";

/** One loan as the desk shows it. */
export type DeskLoan = {
  id: number;
  copy_id: number;
  copy_number: number;
  copy_code: string;
  book_id: number;
  title: string;
  authors: string[] | null;
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
