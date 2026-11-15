import type { ReactElement } from "react";
import { Route, Routes } from "react-router";
import { PatronLayout, StaffLayout, StaffMore } from "../components/Layouts";
import { Placeholder } from "../features/placeholder/Placeholder";
import { adminItems, patronItems, staffFooterItems, staffItems } from "./navigation";

const page = (title: string): ReactElement => <Placeholder title={title} />;

// What the staff tab bar has no room for: its first four items stay in it.
const staffMoreItems = [...staffItems.slice(4), ...adminItems, ...staffFooterItems];

/** The routes of architecture §14. Each page is a placeholder until its feature is built. */
export function AppRoutes() {
  return (
    <Routes>
      <Route element={<PatronLayout items={patronItems} />}>
        <Route index element={page("Home")} />
        <Route path="browse" element={page("Browse")} />
        <Route path="card" element={page("My card")} />
        <Route path="loans" element={page("My books")} />
        <Route path="account" element={page("Account")} />
        <Route path="books/:id" element={page("Book")} />
      </Route>
      <Route
        element={<StaffLayout items={staffItems} adminItems={adminItems} footerItems={staffFooterItems} />}
      >
        <Route path="desk" element={page("Desk")} />
        <Route path="desk/checkout" element={page("Check out")} />
        <Route path="desk/checkin" element={page("Check in")} />
        <Route path="desk/patrons" element={page("Patrons")} />
        <Route path="desk/overdue" element={page("Overdue")} />
        <Route path="catalog" element={page("Catalog")} />
        <Route path="catalog/new" element={page("Add a book")} />
        <Route path="admin/settings" element={page("Settings")} />
        <Route path="admin/people" element={page("People & invites")} />
        <Route path="admin/statistics" element={page("Statistics")} />
        <Route path="more" element={<StaffMore items={staffMoreItems} />} />
      </Route>
      <Route path="setup" element={page("Set up the library")} />
      <Route path="signin" element={page("Sign in")} />
      <Route path="register" element={page("Create your account")} />
      <Route path="*" element={page("Page not found")} />
    </Routes>
  );
}
