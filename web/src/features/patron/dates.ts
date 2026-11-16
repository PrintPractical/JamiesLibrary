// Due dates are calendar dates (YYYY-MM-DD) in the library's time zone, and "today" is today there,
// whatever the device's own zone says.

const zoneOk = (zone: string): boolean => {
  try {
    new Intl.DateTimeFormat("en-CA", { timeZone: zone });
    return true;
  } catch {
    return false;
  }
};

/** Today's date in the library's zone (UTC if the zone is one this browser doesn't know). */
export function today(zone: string, now: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", { timeZone: zoneOk(zone) ? zone : "UTC" }).format(now);
}

/** A calendar date as a moment that falls on that date in every zone. */
const noon = (date: string) => new Date(`${date}T12:00:00Z`);

/** Whole days from `from` to `to`, both YYYY-MM-DD. */
export function daysBetween(from: string, to: string): number {
  return Math.round((noon(to).getTime() - noon(from).getTime()) / 86_400_000);
}

const fmt = (options: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", ...options });
const short = fmt({ weekday: "short", day: "numeric", month: "short" });
const long = fmt({ weekday: "long", day: "numeric", month: "long" });
const withYear = fmt({ day: "numeric", month: "short", year: "numeric" });

/** "Fri 13 Nov" */
export const formatDay = (date: string): string => short.format(noon(date)).replace(",", "");

/** "Monday, 16 November" */
export const formatLongDay = (date: string): string => long.format(noon(date));

/** "13 Nov 2026", for a timestamp (RFC 3339), as the date it fell on in the library's zone. */
export function formatStamp(stamp: string, zone: string): string {
  const at = new Date(stamp);
  if (Number.isNaN(at.getTime())) return stamp;
  return withYear.format(noon(today(zone, at)));
}
