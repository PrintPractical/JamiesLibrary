import { screen, within } from "@testing-library/react";
import { apiError, fakeServer, meOf, renderApp } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const card = { name: "Maya Okafor", number: "123456789012" };

describe("CARD-1 Digital library card", () => {
  test("View card: a patron sees their name, number, a barcode and a QR code", async () => {
    const calls = fakeServer({ "GET /api/me": { body: meOf("patron") }, "GET /api/me/card": { body: card } });
    await renderApp("/card");

    const shown = await screen.findByRole("region", { name: "Library card for Maya Okafor" });
    expect(within(shown).getByText("Maya Okafor")).toBeInTheDocument();
    expect(within(shown).getByText("123456789012")).toHaveClass("mono");
    const barcode = within(shown).getByRole("img", { name: "Barcode for card 123456789012" });
    const qr = within(shown).getByRole("img", { name: "QR code for card 123456789012" });
    // Both images come from the server's own routes, for the signed-in patron only.
    expect(barcode.getAttribute("src")).toMatch(/^\/api\/me\/card\/barcode\.svg\?/);
    expect(qr.getAttribute("src")).toMatch(/^\/api\/me\/card\/qr\.svg\?/);
    expect(calls.filter((c) => c.path === "/api/me/card")).toHaveLength(1);
  });

  test("the card names the library and says what to do if it is lost", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") }, "GET /api/me/card": { body: card } });
    await renderApp("/card");
    const shown = await screen.findByRole("region", { name: /Library card for/ });
    expect(within(shown).getByText("Maple Street Library")).toBeInTheDocument();
    expect(screen.getByText(/Ask a librarian to issue a new one/)).toBeInTheDocument();
  });

  test("a re-issued card is drawn from its new number, so the old picture is not reused", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") }, "GET /api/me/card": { body: { ...card, number: "999999999999" } } });
    await renderApp("/card");
    const barcode = await screen.findByRole("img", { name: "Barcode for card 999999999999" });
    expect(barcode.getAttribute("src")).toContain("card=999999999999");
  });

  test("when the card can't be loaded the patron gets an error and a way to retry", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") }, "GET /api/me/card": apiError(500, "internal", "Something went wrong.") });
    await renderApp("/card");
    expect(await screen.findByRole("button", { name: /try again|retry/i })).toBeInTheDocument();
    expect(screen.queryByRole("img")).toBeNull();
  });
});
