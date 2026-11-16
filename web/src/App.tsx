import { BrowserRouter } from "react-router";
import { AppRoutes } from "./app/routes";
import { SessionProvider } from "./app/session";

export function App() {
  return (
    <SessionProvider>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </SessionProvider>
  );
}
