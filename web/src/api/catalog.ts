import { api } from "./client";

/** A book in a list of results, the same whether a member or a visitor asked. */
export type BookCard = {
  id: number;
  title: string;
  authors: string[];
  isbn: string;
  coverUrl: string;
  total: number;
  available: number;
};

export type SearchPage = { books: BookCard[]; total: number; page: number; perPage: number };

export type Location = { id: number; name: string; copies: number };

export type Copy = {
  id: number;
  number: number;
  condition: string;
  location: string;
  available: boolean;
  /** "YYYY-MM-DD", only on a copy that is out. Never who has it. */
  due_date?: string;
};

export type Review = {
  id: number;
  display_name: string;
  /** 0 when the author left the rating out. */
  rating: number;
  /** "" when the author left the text out. */
  text: string;
  updated_at: string;
};

/** What a member sees on a book's page. */
export type MemberBook = BookCard & {
  kind: "member";
  description: string;
  copies: Copy[];
  ratingAverage: number;
  ratingCount: number;
  myReview: Review | null;
  reviews: Review[];
  reviewsTotal: number;
};

/** What a visitor sees under the public catalog: counts and the average, no copies, reviews or names. */
export type VisitorBook = BookCard & {
  kind: "visitor";
  description: string;
  ratingAverage: number;
  ratingCount: number;
};

export type BookPageData = MemberBook | VisitorBook;

type MemberSummary = { id: number; title: string; authors: string[] | null; isbn: string; cover_url: string; copy_count: number; available: number };
type MemberDetail = MemberSummary & {
  description: string;
  copies: Copy[] | null;
  rating_average: number;
  rating_count: number;
  my_review: Review | null;
  reviews: Review[] | null;
  reviews_total: number;
};
type PublicBook = {
  id: number;
  title: string;
  authors: string[] | null;
  isbn: string;
  description: string;
  cover_url: string;
  available_copies: number;
  total_copies: number;
  rating_average: number;
  rating_count: number;
};
type Paged<T> = { books: T[]; total: number; page: number; page_size: number };

const card = (b: MemberSummary): BookCard => ({
  id: b.id,
  title: b.title,
  authors: b.authors ?? [],
  isbn: b.isbn,
  coverUrl: b.cover_url,
  total: b.copy_count,
  available: b.available,
});

const publicCard = (b: PublicBook): BookCard => ({
  id: b.id,
  title: b.title,
  authors: b.authors ?? [],
  isbn: b.isbn,
  coverUrl: b.cover_url,
  total: b.total_copies,
  available: b.available_copies,
});

function query(params: Record<string, string | number>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== "" && v !== 0) q.set(k, String(v));
  const s = q.toString();
  return s ? `?${s}` : "";
}

/** Search (or browse, with no term) as a signed-in user, optionally in one location (CAT-2, CAT-8). */
export async function searchBooks(q: string, location: number, page: number, signal?: AbortSignal): Promise<SearchPage> {
  const r = await api<Paged<MemberSummary>>(`/api/books${query({ q, location, page: page > 1 ? page : 0 })}`, { signal });
  return { books: r.books.map(card), total: r.total, page: r.page, perPage: r.page_size };
}

/** Search as a visitor, under the public catalog (ACCESS-4). There is no location filter. */
export async function searchPublic(q: string, page: number, signal?: AbortSignal): Promise<SearchPage> {
  const r = await api<Paged<PublicBook>>(`/api/public/books${query({ q, page: page > 1 ? page : 0 })}`, { signal });
  return { books: r.books.map(publicCard), total: r.total, page: r.page, perPage: r.page_size };
}

export const getLocations = (signal?: AbortSignal) => api<Location[]>("/api/locations", { signal });

export async function getBook(id: string, signal?: AbortSignal): Promise<MemberBook> {
  const b = await api<MemberDetail>(`/api/books/${encodeURIComponent(id)}`, { signal });
  return {
    ...card(b),
    kind: "member",
    description: b.description,
    copies: b.copies ?? [],
    ratingAverage: b.rating_average,
    ratingCount: b.rating_count,
    myReview: b.my_review,
    reviews: b.reviews ?? [],
    reviewsTotal: b.reviews_total,
  };
}

export async function getPublicBook(id: string, signal?: AbortSignal): Promise<VisitorBook> {
  const b = await api<PublicBook>(`/api/public/books/${encodeURIComponent(id)}`, { signal });
  return { ...publicCard(b), kind: "visitor", description: b.description, ratingAverage: b.rating_average, ratingCount: b.rating_count };
}

export const getReviews = (bookId: number, page: number, signal?: AbortSignal) =>
  api<{ reviews: Review[] | null; page: number; per_page: number; total: number }>(`/api/books/${bookId}/reviews?page=${page}`, { signal });

/** Saves the caller's own rating and review of a book, replacing what they had. */
export const saveReview = (bookId: number, rating: number | null, text: string) =>
  api<Review>(`/api/books/${bookId}/review`, { method: "PUT", body: { rating, text: text === "" ? null : text } });

export const deleteMyReview = (bookId: number) => api(`/api/books/${bookId}/review`, { method: "DELETE" });

/** A librarian removes someone's review. */
export const removeReview = (reviewId: number) => api(`/api/reviews/${reviewId}`, { method: "DELETE" });

/** What the ISBN lookup answered (SCAN-2). */
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

/** A copy as staff see it, with its barcode. */
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

/** A copy to save. A location is named by ID or, to make one in the same step, by `new_location`. */
export type CopyInput = { location_id: number; new_location?: string; condition: string; sticker?: string };

export const CONDITIONS = ["new", "good", "fair", "worn", "poor", "damaged"] as const;

export const LAYOUTS = [
  { id: "avery5160", label: "Avery 5160 (US Letter, 30 labels)" },
  { id: "averyl7160", label: "Avery L7160 (A4, 21 labels)" },
  { id: "single", label: "One label per page (62 × 29 mm)" },
] as const;

export const lookupIsbn = (isbn: string, signal?: AbortSignal) =>
  api<Lookup>("/api/catalog/lookup", { method: "POST", body: { isbn }, signal });

export const createLocation = (name: string) => api<Location>("/api/locations", { method: "POST", body: { name } });

/** Sends a book as a multipart form when the librarian chose a cover file, anything else as JSON. */
function bookRequest(method: "POST" | "PUT", path: string, input: BookInput): Promise<{ id: number; title: string }> {
  if (input.cover) {
    const form = new FormData();
    form.append("title", input.title);
    for (const a of input.authors) form.append("authors", a);
    if (input.isbn) form.append("isbn", input.isbn);
    form.append("description", input.description);
    form.append("cover", input.cover);
    return api(path, { method, body: form });
  }
  return api(path, {
    method,
    body: {
      title: input.title,
      authors: input.authors,
      isbn: input.isbn,
      description: input.description,
      cover_token: input.coverToken ?? "",
    },
  });
}

/** Saves a new book. */
export const createBook = (input: BookInput) => bookRequest("POST", "/api/books", input);

/** Replaces a book's details; its cover only when one is given. */
export const updateBook = (id: number, input: BookInput) => bookRequest("PUT", `/api/books/${id}`, input);

/** Removes a book from the catalog. Its past loans stay in histories and reports (CAT-5). */
export const removeBook = (id: number) => api(`/api/books/${id}`, { method: "DELETE" });

export const updateCopy = (id: number, copy: CopyInput) => api<StaffCopy>(`/api/copies/${id}`, { method: "PUT", body: copy });

/** Refused while the copy is on loan (CAT-5). */
export const removeCopy = (id: number) => api(`/api/copies/${id}`, { method: "DELETE" });

export const renameLocation = (id: number, name: string) => api<Location>(`/api/locations/${id}`, { method: "PUT", body: { name } });

/** Deletes a location; with `moveTo` its copies move there first, in one step (CAT-8). */
export const deleteLocation = (id: number, moveTo?: number) =>
  api(`/api/locations/${id}${moveTo ? `?move_to=${moveTo}` : ""}`, { method: "DELETE" });

export const addCopy = (bookId: number, copy: CopyInput) =>
  api<StaffCopy>(`/api/books/${bookId}/copies`, { method: "POST", body: copy });

/** The labels of copies as a PDF. */
export const printLabels = (copyIds: number[], layout: string) =>
  api<Blob>("/api/catalog/labels", { method: "POST", body: { copy_ids: copyIds, layout }, blob: true });
