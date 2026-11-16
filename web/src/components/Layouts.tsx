import { useEffect, useState } from "react";
import { Navigate } from "react-router";
import { MoreList, Shell, type NavItem, type ShellProps } from "./Shell";

export type PatronLayoutProps = Pick<ShellProps, "libraryName" | "logoUrl" | "user"> & {
  items: NavItem[];
  /** Staff only: the single link to their first screen, in the side bar's foot and the phone's top bar. */
  staffLink?: NavItem;
};

/** Bottom tab bar on phones, side bar on desktop, the same items in the same order. */
export function PatronLayout({ items, staffLink, ...rest }: PatronLayoutProps) {
  return <Shell items={items} crossLink={staffLink} {...rest} />;
}

export type StaffLayoutProps = Pick<ShellProps, "libraryName" | "logoUrl" | "user"> & {
  items: NavItem[];
  adminItems?: NavItem[];
  /** "My library": the way to the patron side. */
  footerItems?: NavItem[];
};

/** How many staff screens a phone's tab bar shows before "More". */
export const STAFF_TABS = 4;

/** The staff side bar with its Admin group. Phones get four tabs (admin screens count after the staff ones) and "More" for the rest. */
export function StaffLayout({ items, adminItems = [], footerItems = [], ...rest }: StaffLayoutProps) {
  const tabCount = Math.min(items.length + adminItems.length, STAFF_TABS);
  return (
    <Shell
      items={items}
      adminItems={adminItems}
      footerItems={footerItems}
      tabCount={tabCount}
      mode={footerItems[0]}
      {...rest}
    />
  );
}

/** The "More" page: what a phone's tab bar left out. */
export function StaffMore({ items, first }: { items: NavItem[]; first?: string }) {
  const desktop = useDesktop();
  // The side bar already lists everything, so a wide window has nothing to show here.
  if (desktop) return <Navigate to={first ?? "/"} replace />;
  return (
    <>
      <h1>More</h1>
      <MoreList items={items} />
    </>
  );
}

const DESKTOP = "(min-width: 900px)";

function useDesktop(): boolean {
  const query = typeof window.matchMedia === "function" ? window.matchMedia(DESKTOP) : undefined;
  const [desktop, setDesktop] = useState(query?.matches ?? false);
  useEffect(() => {
    if (!query) return;
    const update = () => setDesktop(query.matches);
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, [query]);
  return desktop;
}
