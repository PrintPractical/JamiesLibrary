import type { ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { Icon, Logo as DefaultLogo, type IconName } from "./Icon";

export type NavItem = {
  to: string;
  label: string;
  icon: IconName;
  /** Match only this exact path (for "/"). */
  end?: boolean;
};

export type ShellUser = { name: string; initials: string; roleLabel: string };

export type ShellProps = {
  libraryName?: string;
  /** The library's own logo (an image address on this server); the stock mark when absent. */
  logoUrl?: string;
  /** Side-bar items, and the phone tab bar's, in the same order. */
  items: NavItem[];
  /** Items under the side bar's "Admin" label; the group is left out when empty. */
  adminItems?: NavItem[];
  /** Items pinned to the foot of the side bar. */
  footerItems?: NavItem[];
  /** How many of `items` then `adminItems` the phone tab bar shows before "More". Defaults to all of `items`. */
  tabCount?: number;
  /** A link shown in the phone's top bar (the staff layout's way back to the patron side). */
  mode?: NavItem;
  /** One link to the other side, in the side bar's foot and the phone's top bar. Unlike footerItems it never adds a More tab. */
  crossLink?: NavItem;
  user?: ShellUser;
};

function Logo({ url }: { url?: string }) {
  return url ? <img className="logo" src={url} alt="" /> : <DefaultLogo />;
}

function Tab({ item }: { item: NavItem }) {
  return (
    <NavLink className="tab" to={item.to} end={item.end}>
      <span className="pill">
        <Icon name={item.icon} />
      </span>
      {item.label}
    </NavLink>
  );
}

function SideLink({ item }: { item: NavItem }) {
  return (
    <NavLink className="nav" to={item.to} end={item.end}>
      <Icon name={item.icon} />
      <span>{item.label}</span>
    </NavLink>
  );
}

/** The frame both layouts share: a side bar on desktop, a top bar and pinned tab bar on phones (900px breakpoint). */
export function Shell({
  libraryName = "Jamie's Library",
  logoUrl,
  items,
  adminItems = [],
  footerItems = [],
  tabCount = items.length,
  mode,
  crossLink,
  user,
}: ShellProps) {
  const tabbable = [...items, ...adminItems];
  const tabs = tabbable.slice(0, tabCount);
  const { pathname } = useLocation();
  const hidden = [...tabbable.slice(tabCount), ...footerItems];
  // The More tab is current on /more and on every page reached through it.
  const moreCurrent =
    pathname === "/more" || hidden.some((i) => i.to !== "/" && (pathname === i.to || pathname.startsWith(i.to + "/")));
  return (
    <div className="shell">
      <nav className="side" aria-label="Main">
        <div className="brand">
          <Logo url={logoUrl} />
          <span className="name">{libraryName}</span>
        </div>
        {items.map((item) => (
          <SideLink key={item.to} item={item} />
        ))}
        {adminItems.length > 0 && (
          <>
            <div className="navlabel">Admin</div>
            {adminItems.map((item) => (
              <SideLink key={item.to} item={item} />
            ))}
          </>
        )}
        <div className="grow" />
        {footerItems.map((item) => (
          <SideLink key={item.to} item={item} />
        ))}
        {crossLink && <SideLink item={crossLink} />}
        {user && <Who user={user} />}
      </nav>
      <div className="main">
        <header className="appbar">
          <Logo url={logoUrl} />
          <span className="name">{libraryName}</span>
          <span className="sp" />
          {(mode ?? crossLink) && (
            <NavLink className="mode" to={(mode ?? crossLink)!.to}>
              <Icon name={(mode ?? crossLink)!.icon} />
              {(mode ?? crossLink)!.label}
            </NavLink>
          )}
        </header>
        <main className="content">
          <Outlet />
        </main>
        <nav className="tabbar" aria-label="Main">
          {tabs.map((item) => (
            <Tab key={item.to} item={item} />
          ))}
          {hidden.length > 0 && (
            <Link className="tab" to="/more" aria-current={moreCurrent ? "page" : undefined}>
              <span className="pill">
                <Icon name="more" />
              </span>
              More
            </Link>
          )}
        </nav>
      </div>
    </div>
  );
}

function Who({ user }: { user: ShellUser }) {
  return (
    <div className="who">
      <span className="avatar">{user.initials}</span>
      <span>
        <b>{user.name}</b>
        <br />
        <span className="role">{user.roleLabel}</span>
      </span>
    </div>
  );
}

/** What the phone's "More" tab opens: every item the tab bar had no room for. */
export function MoreList({ items }: { items: NavItem[] }): ReactNode {
  return (
    <nav className="morelist" aria-label="More">
      {items.map((item) => (
        <SideLink key={item.to} item={item} />
      ))}
    </nav>
  );
}
