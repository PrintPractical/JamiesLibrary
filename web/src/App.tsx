import { BrowserRouter } from "react-router";
import { AppRoutes } from "./app/routes";
import { ThemeProvider } from "./app/ThemeProvider";

export function App() {
  return (
    <ThemeProvider>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </ThemeProvider>
  );
}
