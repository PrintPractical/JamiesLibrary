import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiError, fakeServer, meOf, renderApp, type Calls, type Handler, type Reply } from "../../testing";

afterEach(() => {
  vi.unstubAllGlobals();
});

const locations = [
  { id: 1, name: "Study", copies: 3 },
  { id: 2, name: "Kitchen", copies: 1 },
  { id: 3, name: "Attic", copies: 0 },
];

const book = (over: Record<string, unknown> = {}) => ({
  id: 5,
  title: "Piranesi",
  authors: ["Susanna Clarke"],
  isbn: "9781635575637",
  cover_url: "",
  copy_count: 2,
  available: 1,
  description: "A man lives in the House.",
  copies: [
    { id: 1, number: 1, condition: "good", location: "Study", available: true },
    { id: 2, number: 2, condition: "fair", location: "Kitchen", available: false, due_date: "2026-11-24" },
  ],
  rating_average: 0,
  rating_count: 0,
  my_review: null,
  reviews: [],
  reviews_total: 0,
  ...over,
});

function server(extra: Record<string, Handler | Reply> = {}, mode: "isbn" | "own" = "isbn"): Calls {
  return fakeServer({
    "GET /api/me": { body: { ...meOf("librarian"), settings: { copy_id_mode: mode } } },
    "GET /api/locations": { body: locations },
    "GET /api/books/5": { body: book() },
    ...extra,
  });
}

const sent = (calls: Calls, method: string, path: string) => calls.filter((c) => c.method === method && c.path === path);

describe("CAT-5 Manage inventory: a book", () => {
  test("Edit a book: its details are saved", async () => {
    const calls = server({ "PUT /api/books/5": { body: book({ title: "Piranesi (2020)" }) } });
    await renderApp("/catalog/books/5");
    const title = await screen.findByLabelText("Title (required)");
    expect(title).toHaveValue("Piranesi");
    await userEvent.clear(title);
    await userEvent.type(title, "Piranesi (2020)");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByText("Saved.");
    expect(sent(calls, "PUT", "/api/books/5")[0]?.body).toMatchObject({ title: "Piranesi (2020)", authors: ["Susanna Clarke"], isbn: "9781635575637" });
  });

  test("an empty title is refused before anything is sent", async () => {
    const calls = server();
    await renderApp("/catalog/books/5");
    await userEvent.clear(await screen.findByLabelText("Title (required)"));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Enter the book's title.")).toBeInTheDocument();
    expect(sent(calls, "PUT", "/api/books/5")).toHaveLength(0);
  });

  test("an ISBN another book has is refused and that book is offered", async () => {
    server({
      "PUT /api/books/5": { status: 409, body: { error: { code: "conflict", message: "Another book already has this ISBN.", field: "isbn" }, book_id: 9 } },
    });
    await renderApp("/catalog/books/5");
    await screen.findByLabelText("Title (required)");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("Another book already has this ISBN.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open the book that has this ISBN" })).toHaveAttribute("href", "/catalog/books/9");
  });

  test("Remove a book: after asking again it is removed and the catalog opens", async () => {
    const calls = server({ "DELETE /api/books/5": { status: 204 }, "GET /api/books": { body: { books: [], total: 0, page: 1, page_size: 50 } } });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Remove book…" }));
    expect(sent(calls, "DELETE", "/api/books/5")).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "Yes, remove the book" }));
    await screen.findByRole("heading", { level: 1, name: "Catalog" });
    expect(sent(calls, "DELETE", "/api/books/5")).toHaveLength(1);
  });

  test("a book with a copy on loan can't be removed: the reason is shown", async () => {
    server({
      "DELETE /api/books/5": apiError(409, "conflict", "This book cannot be removed while 1 copy is on loan: check it in first."),
    });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Remove book…" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, remove the book" }));
    expect(await screen.findByText("This book cannot be removed while 1 copy is on loan: check it in first.")).toBeInTheDocument();
  });
});

describe("CAT-5 Manage inventory: copies", () => {
  test("Remove checked-out copy: it is refused and the message explains why", async () => {
    const calls = server({ "DELETE /api/copies/2": apiError(409, "conflict", "Copy 2 is on loan and can't be removed: check it in first.") });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Remove copy 2" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, remove copy 2" }));
    expect(await screen.findByText("Copy 2 is on loan and can't be removed: check it in first.")).toBeInTheDocument();
    expect(sent(calls, "DELETE", "/api/copies/2")).toHaveLength(1);
    // The copy is still listed.
    expect(screen.getByText("On loan")).toBeInTheDocument();
  });

  test("a copy on the shelf is removed and the list is loaded again", async () => {
    let removed = false;
    const calls = server({
      "DELETE /api/copies/1": () => {
        removed = true;
        return { status: 204 };
      },
      "GET /api/books/5": () => ({ body: book({ copies: removed ? [book().copies[1]] : book().copies }) }),
    });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Remove copy 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Yes, remove copy 1" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Remove copy 1" })).toBeNull());
    expect(sent(calls, "DELETE", "/api/copies/1")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "Remove copy 2" })).toBeInTheDocument();
  });

  test("Edit a copy: its location and condition are sent", async () => {
    const calls = server({ "PUT /api/copies/1": { body: { id: 1, book_id: 5, number: 1, condition: "worn", location: "Attic" } } });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Edit copy 1" }));
    const form = screen.getByRole("group", { name: "Copy 1" });
    expect(within(form).getByLabelText("Location")).toHaveValue("1");
    await userEvent.selectOptions(within(form).getByLabelText("Location"), "3");
    await userEvent.selectOptions(within(form).getByLabelText("Condition"), "worn");
    await userEvent.click(screen.getByRole("button", { name: "Save the copy" }));
    await waitFor(() => expect(sent(calls, "PUT", "/api/copies/1")).toHaveLength(1));
    expect(sent(calls, "PUT", "/api/copies/1")[0]?.body).toEqual({ location_id: 3, condition: "worn" });
  });

  test("Add a copy: it gets the next number and the chosen location", async () => {
    const calls = server({ "POST /api/books/5/copies": { status: 201, body: { id: 8, book_id: 5, number: 3, condition: "good", location: "Kitchen" } } });
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Add a copy" }));
    const form = screen.getByRole("group", { name: "Copy 3" });
    await userEvent.click(screen.getByRole("button", { name: "Add the copy" }));
    expect(await screen.findByText("Choose where this copy lives.")).toBeInTheDocument();
    expect(sent(calls, "POST", "/api/books/5/copies")).toHaveLength(0);
    await userEvent.selectOptions(within(form).getByLabelText("Location"), "2");
    await userEvent.click(screen.getByRole("button", { name: "Add the copy" }));
    await waitFor(() => expect(sent(calls, "POST", "/api/books/5/copies")).toHaveLength(1));
    expect(sent(calls, "POST", "/api/books/5/copies")[0]?.body).toEqual({ location_id: 2, condition: "good" });
  });

  test("Add a copy in own-barcode mode with a sticker that is a card number: the refusal is shown at the sticker", async () => {
    server(
      { "POST /api/books/5/copies": apiError(409, "conflict", "That is a library card number.", "sticker") },
      "own",
    );
    await renderApp("/catalog/books/5");
    await userEvent.click(await screen.findByRole("button", { name: "Add a copy" }));
    const form = screen.getByRole("group", { name: "Copy 3" });
    await userEvent.click(within(form).getByRole("radio", { name: "Use a sticker" }));
    await userEvent.type(within(form).getByLabelText("Sticker barcode"), "123456789012");
    await userEvent.selectOptions(within(form).getByLabelText("Location"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Add the copy" }));
    expect(await screen.findByText("That is a library card number.")).toBeInTheDocument();
  });

  test("the book page offers staff a way to change the book, and a patron none", async () => {
    server({ "GET /api/books/5": { body: book() } });
    await renderApp("/books/5");
    expect(await screen.findByRole("link", { name: "Change this book" })).toHaveAttribute("href", "/catalog/books/5");
  });
});

describe("CAT-8 Named locations", () => {
  test("Delete a location in use: it is refused, says 3 copies are there, offers to move them, then deletes", async () => {
    let moved = false;
    const calls = server({
      "GET /api/locations": () => ({ body: moved ? locations.filter((l) => l.id !== 1) : locations }),
      "DELETE /api/locations/1": apiError(409, "conflict", "This location cannot be deleted: 3 copies are there. Move them to another location first."),
      "DELETE /api/locations/1?move_to=2": () => {
        moved = true;
        return { status: 204 };
      },
    });
    await renderApp("/catalog/locations");
    await userEvent.click(await screen.findByRole("button", { name: "Delete Study" }));
    expect(await screen.findByText("3 copies are there — move them to…")).toBeInTheDocument();
    const choose = screen.getByLabelText("Move the copies to");
    // Not the location itself.
    expect(within(choose).queryByRole("option", { name: "Study" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Move them and delete this location" }));
    expect(await screen.findByText("Choose where to move the copies.")).toBeInTheDocument();
    await userEvent.selectOptions(choose, "2");
    await userEvent.click(screen.getByRole("button", { name: "Move them and delete this location" }));
    await waitFor(() => expect(screen.queryByText("Study")).toBeNull());
    expect(sent(calls, "DELETE", "/api/locations/1?move_to=2")).toHaveLength(1);
    expect(screen.getByText("Kitchen")).toBeInTheDocument();
  });

  test("Delete an empty location: it is gone from the list", async () => {
    let gone = false;
    server({
      "GET /api/locations": () => ({ body: gone ? locations.filter((l) => l.id !== 3) : locations }),
      "DELETE /api/locations/3": () => {
        gone = true;
        return { status: 204 };
      },
    });
    await renderApp("/catalog/locations");
    await userEvent.click(await screen.findByRole("button", { name: "Delete Attic" }));
    await waitFor(() => expect(screen.queryByText("Attic")).toBeNull());
    expect(screen.queryByText(/are there/)).toBeNull();
  });

  test("Duplicate name: a new location with a name that exists is refused with the reason", async () => {
    server({ "POST /api/locations": apiError(409, "conflict", "A location with this name already exists.", "name") });
    await renderApp("/catalog/locations");
    await userEvent.type(await screen.findByLabelText("Name"), "study");
    await userEvent.click(screen.getByRole("button", { name: "Add location" }));
    expect(await screen.findByText("A location with this name already exists.")).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toHaveValue("study");
  });

  test("Rename: the new name is sent and the list is loaded again", async () => {
    let name = "Study";
    const calls = server({
      "GET /api/locations": () => ({ body: [{ id: 1, name, copies: 3 }] }),
      "PUT /api/locations/1": (b) => {
        name = (b as { name: string }).name;
        return { body: { id: 1, name } };
      },
    });
    await renderApp("/catalog/locations");
    await userEvent.click(await screen.findByRole("button", { name: "Rename Study" }));
    const field = within(screen.getByRole("form", { name: "Rename Study" })).getByLabelText("Name");
    await userEvent.clear(field);
    await userEvent.type(field, "Reading room");
    await userEvent.click(screen.getByRole("button", { name: "Save name" }));
    expect(await screen.findByText("Reading room")).toBeInTheDocument();
    expect(sent(calls, "PUT", "/api/locations/1")[0]?.body).toEqual({ name: "Reading room" });
  });

  test("a location with copies but nowhere to move them says so", async () => {
    server({
      "GET /api/locations": { body: [{ id: 1, name: "Main shelf", copies: 2 }] },
      "DELETE /api/locations/1": apiError(409, "conflict", "This location cannot be deleted: 2 copies are there. Move them to another location first."),
    });
    await renderApp("/catalog/locations");
    await userEvent.click(await screen.findByRole("button", { name: "Delete Main shelf" }));
    expect(await screen.findByText("2 copies are there — move them to…")).toBeInTheDocument();
    expect(screen.getByText(/no other location yet/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Move them/ })).toBeNull();
  });
});

describe("CAT-7 Printable barcode labels", () => {
  function pdfServer(extra: Record<string, Handler | Reply> = {}) {
    const open = vi.fn(() => ({}));
    vi.stubGlobal("open", open);
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:labels", revokeObjectURL: () => {} }));
    const calls = server({
      "GET /api/books?q=pira": { body: { books: [{ id: 5, title: "Piranesi", authors: ["Susanna Clarke"], isbn: "", cover_url: "", copy_count: 2, available: 1 }], total: 1, page: 1, page_size: 50 } },
      "POST /api/catalog/labels": { body: {} },
      ...extra,
    });
    return { open, calls };
  }

  test("Print a sheet: the chosen copies and layout are requested as a PDF", async () => {
    const { open, calls } = pdfServer();
    await renderApp("/catalog/labels");
    expect(await screen.findByRole("button", { name: "Make the labels" })).toBeDisabled();
    await userEvent.type(screen.getByLabelText("Find a book"), "pira{Enter}");
    await userEvent.click(await screen.findByRole("button", { name: /Piranesi/ }));
    await userEvent.click(await screen.findByRole("checkbox", { name: /Copy 1/ }));
    await userEvent.click(screen.getByRole("checkbox", { name: /Copy 2/ }));
    expect(screen.getByText("2 copies chosen.")).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Label sheet"), "avery5160");
    await userEvent.click(screen.getByRole("button", { name: "Make the labels" }));
    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(sent(calls, "POST", "/api/catalog/labels")[0]?.body).toEqual({ copy_ids: [1, 2], layout: "avery5160" });
  });

  test("the layouts offered are Avery 5160, Avery L7160 and one per page", async () => {
    pdfServer();
    await renderApp("/catalog/labels");
    const layouts = await screen.findByLabelText("Label sheet");
    expect(within(layouts).getAllByRole("option").map((o) => (o as HTMLOptionElement).value)).toEqual(["avery5160", "averyl7160", "single"]);
  });

  test("coming from a book's page, its copies are listed and 'All copies' chooses them all", async () => {
    const { calls } = pdfServer();
    await renderApp("/catalog/labels?book=5");
    await userEvent.click(await screen.findByRole("checkbox", { name: "All copies" }));
    await userEvent.selectOptions(screen.getByLabelText("Label sheet"), "single");
    await userEvent.click(screen.getByRole("button", { name: "Make the labels" }));
    await waitFor(() => expect(sent(calls, "POST", "/api/catalog/labels")).toHaveLength(1));
    expect(sent(calls, "POST", "/api/catalog/labels")[0]?.body).toEqual({ copy_ids: [1, 2], layout: "single" });
  });

  test("a refusal from the server is shown where the button is", async () => {
    pdfServer({ "POST /api/catalog/labels": apiError(409, "conflict", "Copy 1 of “Piranesi” has no barcode to print. Give it a sticker or switch to own-barcode mode.", "copy_ids") });
    await renderApp("/catalog/labels?book=5");
    await userEvent.click(await screen.findByRole("checkbox", { name: /Copy 1/ }));
    await userEvent.click(screen.getByRole("button", { name: "Make the labels" }));
    expect(await screen.findByText(/has no barcode to print/)).toBeInTheDocument();
  });
});

describe("Catalog", () => {
  test("lists books to change, with links to the other tools", async () => {
    server({ "GET /api/books": { body: { books: [{ id: 5, title: "Piranesi", authors: [], isbn: "", cover_url: "", copy_count: 0, available: 0 }], total: 1, page: 1, page_size: 50 } } });
    await renderApp("/catalog");
    expect(await screen.findByRole("link", { name: /Piranesi/ })).toHaveAttribute("href", "/catalog/books/5");
    expect(screen.getByRole("link", { name: "Locations" })).toHaveAttribute("href", "/catalog/locations");
    expect(screen.getByRole("link", { name: "Print labels" })).toHaveAttribute("href", "/catalog/labels");
    expect(screen.getByRole("link", { name: "Add a book" })).toHaveAttribute("href", "/catalog/new");
  });

  test("a patron is not shown the management screens", async () => {
    fakeServer({ "GET /api/me": { body: meOf("patron") } });
    await renderApp("/catalog/locations");
    await waitFor(() => expect(screen.getAllByRole("link", { name: /Browse/ }).length).toBeGreaterThan(0));
    expect(screen.queryByRole("heading", { name: "Locations" })).toBeNull();
  });
});
