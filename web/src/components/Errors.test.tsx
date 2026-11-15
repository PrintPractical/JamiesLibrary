import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError, NetworkError } from "../api/client";
import { InlineError, PageError } from "./Errors";

test("InlineError shows an API error's own message as an alert, and nothing for no error", () => {
  const err = new ApiError({ status: 409, code: "conflict", message: "This copy is already on loan." });
  const { rerender, container } = render(<InlineError error={err} />);
  expect(screen.getByRole("alert")).toHaveTextContent("This copy is already on loan.");
  rerender(<InlineError error={null} />);
  expect(container).toBeEmptyDOMElement();
});

test("PageError says what failed and Retry calls back", async () => {
  const retry = vi.fn();
  render(<PageError error={new NetworkError()} onRetry={retry} />);
  expect(screen.getByRole("alert")).toHaveTextContent(/can't reach the library/i);
  await userEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(retry).toHaveBeenCalledTimes(1);
});

test("an error that isn't the app's own gets a plain sentence, never its internals", () => {
  render(<InlineError error={new TypeError("x is undefined at line 4")} />);
  expect(screen.getByRole("alert")).not.toHaveTextContent("undefined");
});
