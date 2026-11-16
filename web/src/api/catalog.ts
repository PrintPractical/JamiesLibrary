import { api } from "./client";

export type Lookup = {
  status: "found" | "exists" | "not_found";
  book_id?: number;
  isbn?: string;
  title?: string;
  authors?: string[];
  description?: string;
  cover_token?: string;
  cover_url?: string;
};

export type CopyOfBook = { id: number; number: number; condition: string; location: string; available: boolean };

export type Book = {
  id: number;
  title: string;
  authors: string[];
  isbn: string;
  cover_url: string;
  copy_count: number;
  available: number;
  description: string;
  copies: CopyOfBook[];
};

export type Location = { id: number; name: string; copies: number };

export type StaffCopy = { id: number; book_id: number; number: number; condition: string; location: string; barcode: string };

export type BookInput = {
  title: string;
  authors: string[];
  isbn: string;
  description: string;
  /** A cover fetched by the lookup. */
  coverToken?: string;
  /** A cover the librarian chose. */
  cover?: File;
};

export type CopyInput = { location_id: number; condition: string; sticker?: string };

export const CONDITIONS = ["new", "good", "fair", "worn", "poor", "damaged"] as const;

export const LAYOUTS = [
  { id: "avery5160", label: "Avery 5160 (US Letter, 30 labels)" },
  { id: "averyl7160", label: "Avery L7160 (A4, 21 labels)" },
  { id: "single", label: "One label per page (62 × 29 mm)" },
] as const;

export const lookupIsbn = (isbn: string, signal?: AbortSignal) =>
  api<Lookup>("/api/catalog/lookup", { method: "POST", body: { isbn }, signal });

export const getBook = (id: number, signal?: AbortSignal) => api<Book>(`/api/books/${id}`, { signal });

export const getLocations = (signal?: AbortSignal) => api<Location[]>("/api/locations", { signal });

export const createLocation = (name: string) => api<Location>("/api/locations", { method: "POST", body: { name } });

/** Saves a new book. A cover the librarian chose goes as a multipart form, anything else as JSON. */
export function createBook(input: BookInput): Promise<Book> {
  if (input.cover) {
    const form = new FormData();
    form.append("title", input.title);
    for (const a of input.authors) form.append("authors", a);
    if (input.isbn) form.append("isbn", input.isbn);
    form.append("description", input.description);
    form.append("cover", input.cover);
    return api<Book>("/api/books", { method: "POST", body: form });
  }
  return api<Book>("/api/books", {
    method: "POST",
    body: {
      title: input.title,
      authors: input.authors,
      isbn: input.isbn,
      description: input.description,
      cover_token: input.coverToken ?? "",
    },
  });
}

export const addCopy = (bookId: number, copy: CopyInput) =>
  api<StaffCopy>(`/api/books/${bookId}/copies`, { method: "POST", body: copy });

/** The labels of copies as a PDF. */
export const printLabels = (copyIds: number[], layout: string) =>
  api<Blob>("/api/catalog/labels", { method: "POST", body: { copy_ids: copyIds, layout }, blob: true });
