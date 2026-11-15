import { MoreList, Shell, type NavItem, type ShellProps } from "./Shell";

export type PatronLayoutProps = Pick<ShellProps, "libraryName" | "user"> & { items: NavItem[] };

/** Bottom tab bar on phones, side bar on desktop, the same items in the same order. */
export function PatronLayout({ items, ...rest }: PatronLayoutProps) {
  return <Shell items={items} {...rest} />;
}

export type StaffLayoutProps = Pick<ShellProps, "libraryName" | "user"> & {
  items: NavItem[];
  adminItems?: NavItem[];
  /** "My library": the way to the patron side. */
  footerItems?: NavItem[];
};

/** The staff side bar with its Admin group. Phones get four tabs and "More" for the rest. */
export function StaffLayout({ items, adminItems = [], footerItems = [], ...rest }: StaffLayoutProps) {
  const tabCount = Math.min(items.length, 4);
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
export function StaffMore({ items }: { items: NavItem[] }) {
  return (
    <>
      <h1>More</h1>
      <MoreList items={items} />
    </>
  );
}
