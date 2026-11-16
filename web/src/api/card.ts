import { api } from "./client";

export type OwnCard = { name: string; number: string };

export const getOwnCard = (signal?: AbortSignal) => api<OwnCard>("/api/me/card", { signal });

/** The card's images. The number in the query keeps a re-issued card from showing a cached picture of the old one. */
export const cardImage = (kind: "barcode" | "qr", number: string) =>
  `/api/me/card/${kind}.svg?card=${encodeURIComponent(number)}`;
