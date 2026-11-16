/** Formats in the library's time zone; a zone the browser doesn't know falls back to the browser's own. */
function formatter(zone: string | undefined, options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  try {
    return new Intl.DateTimeFormat(undefined, { ...options, timeZone: zone });
  } catch {
    return new Intl.DateTimeFormat(undefined, options);
  }
}

/** A moment as people read it, in the library's zone ("16 Nov 2026, 09:30"). */
export function timeText(iso: string, zone: string | undefined): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return formatter(zone, { day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" }).format(d);
}

/** The calendar date (YYYY-MM-DD) `daysAgo` days before now, in the library's zone. */
export function dayText(now: Date, zone: string | undefined, daysAgo = 0): string {
  const parts = formatter(zone, { year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(now);
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  const d = new Date(Date.UTC(get("year"), get("month") - 1, get("day") - daysAgo));
  return d.toISOString().slice(0, 10);
}
