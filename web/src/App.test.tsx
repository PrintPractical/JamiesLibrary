import { render, screen } from "@testing-library/react";
import { App } from "./App";

test("says the app's name", () => {
  render(<App />);
  expect(screen.getByRole("heading", { level: 1, name: "Jamie's Library" })).toBeInTheDocument();
});
