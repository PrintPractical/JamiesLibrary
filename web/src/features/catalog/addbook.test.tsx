import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router";
import type { CodeDetector } from "../../components/barcodeDetector";
import { SessionProvider } from "../../app/session";
import { apiError, fakeServer, meOf, renderApp, type Calls, type Handler, type Reply } from "../../testing";
import { AddBook } from "./AddBook";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const ISBN = "9780064404990";
const found = {
  status: "found",
  isbn: ISBN,
  title: "The Lion, the Witch and the Wardrobe",
  authors: ["C. S. Lewis"],
  description: "Four siblings step through a wardrobe.",
  cover_token: "tok1",
  cover_url: "/media-tmp/tok1",
};
const locations = [
  { id: 1, name: "Kids' room · Top shelf", copies: 3 },
  { id: 2, name: "Living room · Shelf A1", copies: 5 },
];
const bookOf = (id: number, over = {}) => ({
  id,
  title: found.title,
  authors: found.authors,
  isbn: ISBN,
  cover_url: "",
  copy_count: 1,
  available: 1,
  description: "",
  copies: [{ id: 9, number: 1, condition: "good", location: "Kids' room", available: true }],
  ...over,
});

function server(extra: Record<string, Handler | Reply> = {}, mode: "isbn" | "own" = "isbn") {
  return fakeServer({
    "GET /api/me": { body: { ...meOf("librarian"), settings: { copy_id_mode: mode } } },
    "GET /api/locations": { body: locations },
    ...extra,
  });
}

async function open(detectorFactory?: () => Promise<CodeDetector>) {
  render(
    <SessionProvider>
      <MemoryRouter initialEntries={["/catalog/new"]}>
        <Routes>
          <Route path="catalog/new" element={<AddBook detectorFactory={detectorFactory} />} />
          <Route path="catalog" element={<p>The catalog</p>} />
        </Routes>
      </MemoryRouter>
    </SessionProvider>,
  );
  return screen.findByLabelText("ISBN");
}

const lookupWith = async (isbn: string) => {
  await userEvent.type(await open(), `${isbn}{Enter}`);
};
const copyCard = (n: number) => screen.getByRole("group", { name: `Copy ${n}` });
const posted = (calls: Calls, path: string) =>
  calls.filter((c) => c.method === "POST" && c.path === path);

function fakeCamera(reject?: string) {
  const track = { stop: vi.fn(), onended: null, getCapabilities: () => ({}) };
  const media = { getTracks: () => [track], getVideoTracks: () => [track] };
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  Object.defineProperty(navigator, "mediaDevices", {
    value: {
      getUserMedia: async () => {
        if (reject) throw new DOMException("no", reject);
        return media;
      },
      enumerateDevices: async () => [{ kind: "videoinput" }],
    },
    configurable: true,
  });
  vi.spyOn(HTMLMediaElement.prototype, "readyState", "get").mockReturnValue(4);
  vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
}

describe("SCAN-1 Scan ISBN to add", () => {
  test("Scan ISBN: a code the decoder reads fills the ISBN and starts the lookup", async () => {
    fakeCamera();
    const calls = server({ "POST /api/catalog/lookup": { body: found } });
    const detector: CodeDetector = { detect: async () => [{ rawValue: ISBN }] } as CodeDetector;
    await open(async () => detector);
    await userEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
    expect(await screen.findByText("Found on Open Library")).toBeInTheDocument();
    expect(screen.getByLabelText("ISBN")).toHaveValue(ISBN);
    expect(posted(calls, "/api/catalog/lookup").map((c) => c.body)).toEqual([{ isbn: ISBN }]);
    expect(screen.getByLabelText("Title (required)")).toHaveValue(found.title);
  });

  test("Unreadable barcode: when the camera can't be used the ISBN is typed instead", async () => {
    fakeCamera("NotAllowedError");
    const calls = server({ "POST /api/catalog/lookup": { body: found } });
    await open();
    await userEvent.click(await screen.findByRole("button", { name: "Scan with the camera" }));
    expect((await screen.findAllByRole("status"))[0]).toHaveTextContent(/blocked/i);
    await userEvent.type(screen.getByLabelText("ISBN"), `${ISBN}{Enter}`);
    expect(await screen.findByText("Found on Open Library")).toBeInTheDocument();
    expect(posted(calls, "/api/catalog/lookup")).toHaveLength(1);
  });

  test("an ISBN the server refuses is said where it was typed and no form opens", async () => {
    server({ "POST /api/catalog/lookup": apiError(422, "invalid", "That is not a valid ISBN. Check the digits.", "isbn") });
    await lookupWith("12345");
    expect(await screen.findByRole("alert")).toHaveTextContent("not a valid ISBN");
    expect(screen.queryByLabelText("Title (required)")).toBeNull();
  });
});

describe("SCAN-2 Metadata lookup", () => {
  test("Found: the form is prefilled, nothing is saved until Add, and Add saves the book and its copy", async () => {
    const calls = server({
      "POST /api/catalog/lookup": { body: found },
      "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
      "POST /api/books/31/copies": { status: 201, body: { id: 70, book_id: 31, number: 1, condition: "good", barcode: "" } },
    });
    await lookupWith(ISBN);
    expect(await screen.findByText("Check the details below. Nothing is saved until you press Add.")).toBeInTheDocument();
    expect(screen.getByLabelText("Authors (required)")).toHaveValue("C. S. Lewis");
    expect(screen.getByLabelText("Description")).toHaveValue(found.description);
    expect(document.querySelector(".ab-cover img")).toHaveAttribute("src", "/media-tmp/tok1");
    expect(calls.filter((c) => c.method !== "GET" && c.path !== "/api/catalog/lookup")).toEqual([]);

    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "Kids' room · Top shelf");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await screen.findByText(/is in the catalog with 1 copy/)).toBeInTheDocument();
    expect(posted(calls, "/api/books")[0]?.body).toEqual({
      title: found.title,
      authors: ["C. S. Lewis"],
      isbn: ISBN,
      description: found.description,
      cover_token: "tok1",
    });
    expect(posted(calls, "/api/books/31/copies")[0]?.body).toEqual({ location_id: 1, condition: "good" });
  });

  test("Not found: the librarian is told and the form is empty and editable", async () => {
    server({ "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } } });
    await lookupWith(ISBN);
    expect(await screen.findByText("Not found on Open Library")).toBeInTheDocument();
    const title = screen.getByLabelText("Title (required)");
    expect(title).toHaveValue("");
    await userEvent.type(title, "A Hand-Written Book");
    expect(title).toHaveValue("A Hand-Written Book");
  });

  test("Provider down: the librarian is told the lookup is unavailable and can still type the details", async () => {
    const calls = server({ "POST /api/catalog/lookup": apiError(503, "unavailable", "Lookup unavailable, enter the details.") });
    await lookupWith(ISBN);
    expect(await screen.findByText("Lookup unavailable")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("Title (required)"), "By Hand");
    expect(screen.getByLabelText("ISBN")).toHaveValue(ISBN);
    expect(calls.filter((c) => c.method === "POST" && c.path === "/api/books")).toHaveLength(0);
  });

  test("ISBN already catalogued: the existing book is shown, a copy is offered, and no book is made", async () => {
    const calls = server({
      "POST /api/catalog/lookup": { body: { status: "exists", book_id: 12, isbn: ISBN } },
      "GET /api/books/12": { body: bookOf(12) },
      "POST /api/books/12/copies": { status: 201, body: { id: 71, book_id: 12, number: 2, condition: "good", barcode: "" } },
    });
    await lookupWith(ISBN);
    expect(await screen.findByText("This book is already in the catalog")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: found.title })).toHaveAttribute("href", "/books/12");
    expect(screen.queryByLabelText("Title (required)")).toBeNull();
    expect(copyCard(2)).toBeInTheDocument();
    await userEvent.selectOptions(within(copyCard(2)).getByLabelText("Location"), "Living room · Shelf A1");
    await userEvent.click(screen.getByRole("button", { name: "Add 1 copy" }));
    expect(await screen.findByText(`1 copy added to “${found.title}”.`)).toBeInTheDocument();
    expect(screen.queryByText(/is in the catalog with/)).toBeNull();
    expect(posted(calls, "/api/books")).toHaveLength(0);
    expect(posted(calls, "/api/books/12/copies")[0]?.body).toEqual({ location_id: 2, condition: "good" });
  });

  test("Add without a title, or a copy without a location, is refused before anything is sent", async () => {
    const calls = server({ "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } } });
    await lookupWith(ISBN);
    await userEvent.click(await screen.findByRole("button", { name: "Add book & 1 copy" }));
    expect(screen.getByText("Enter the book's title.")).toBeInTheDocument();
    expect(screen.getByText("Choose where this copy lives.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST" && c.path !== "/api/catalog/lookup")).toEqual([]);
  });

  test("a refused Add moves focus to the first field in error: Title, Authors, then a copy's Location", async () => {
    server({ "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } } });
    await lookupWith(ISBN);
    await userEvent.click(await screen.findByRole("button", { name: "Add book & 1 copy" }));
    const title = screen.getByLabelText("Title (required)");
    expect(title).toHaveFocus();
    expect(title).toHaveAttribute("aria-invalid", "true");
    await userEvent.type(title, "A title");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(screen.getByLabelText("Authors (required)")).toHaveFocus();
    await userEvent.type(screen.getByLabelText("Authors (required)"), "An Author");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(within(copyCard(1)).getByLabelText("Location")).toHaveFocus();
  });

  test("a cover that is not an image of an allowed kind is refused with a message and nothing is sent", async () => {
    const calls = server({ "POST /api/catalog/lookup": { body: found } });
    await lookupWith(ISBN);
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:cover", revokeObjectURL: () => {} }));
    const user = userEvent.setup({ applyAccept: false }); // the file picker would hide a GIF: a drag or a wrong pick may not
    await user.upload(await screen.findByLabelText("Cover image file"), new File(["x"], "c.gif", { type: "image/gif" }));
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await screen.findByText("A cover must be a JPEG, PNG or WebP image.")).toBeInTheDocument();
    expect(posted(calls, "/api/books")).toHaveLength(0);
  });
});

describe("Entering a book without an ISBN", () => {
  test("a book with no ISBN carries its own barcode even in by-ISBN mode, and is saved with no ISBN", async () => {
    const calls = server({
      "POST /api/books": { status: 201, body: bookOf(40, { isbn: "", copies: [] }) },
      "POST /api/books/40/copies": { status: 201, body: { id: 80, book_id: 40, number: 1, condition: "good", barcode: "C000080" } },
    });
    await open();
    await userEvent.click(screen.getByRole("button", { name: "Enter without an ISBN" }));
    await userEvent.type(screen.getByLabelText("Title (required)"), "Old Book");
    await userEvent.type(screen.getByLabelText("Authors (required)"), "A. Writer, B. Writer");
    expect(within(copyCard(1)).getByRole("radio", { name: "New label" })).toBeChecked();
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "2");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await screen.findByText(/C000080/)).toBeInTheDocument();
    expect(posted(calls, "/api/books")[0]?.body).toMatchObject({ isbn: "", authors: ["A. Writer", "B. Writer"] });
  });
});

test("Enter without an ISBN after a lookup empties the ISBN field and the book is saved with none", async () => {
  const calls = server({
    "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } },
    "POST /api/books": { status: 201, body: bookOf(40, { isbn: "", copies: [] }) },
    "POST /api/books/40/copies": { status: 201, body: { id: 80, book_id: 40, number: 1, condition: "good", barcode: "C000080" } },
  });
  await lookupWith(ISBN);
  await screen.findByText("Not found on Open Library");
  expect(screen.getByLabelText("ISBN")).toHaveValue(ISBN);
  await userEvent.click(screen.getByRole("button", { name: "Enter without an ISBN" }));
  expect(screen.getByLabelText("ISBN")).toHaveValue("");
  await userEvent.type(screen.getByLabelText("Title (required)"), "Old Book");
  await userEvent.type(screen.getByLabelText("Authors (required)"), "A. Writer");
  await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
  await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
  await screen.findByText(/C000080/);
  expect(posted(calls, "/api/books")[0]?.body).toMatchObject({ isbn: "" });
});

test("a missing location is said under the Location field", async () => {
  server({ "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } } });
  await lookupWith(ISBN);
  await userEvent.type(await screen.findByLabelText("Title (required)"), "X");
  await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
  const select = within(copyCard(1)).getByLabelText("Location");
  expect(select).toHaveAccessibleDescription("Choose where this copy lives.");
});

describe("CAT-9 How copies are identified", () => {
  test("Default: a book with an ISBN in by-ISBN mode asks for no barcode", async () => {
    server({ "POST /api/catalog/lookup": { body: found } });
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    expect(screen.queryByRole("radio")).toBeNull();
    expect(screen.queryByLabelText("Sticker barcode")).toBeNull();
    expect(screen.getByText(/share its ISBN barcode/)).toBeInTheDocument();
  });

  test("in own-barcode mode each copy may take a new label or a sticker", async () => {
    server({ "POST /api/catalog/lookup": { body: found } }, "own");
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    expect(within(copyCard(1)).getByRole("radio", { name: "New label" })).toBeChecked();
    await userEvent.click(within(copyCard(1)).getByRole("radio", { name: "Use a sticker" }));
    expect(screen.getByLabelText("Sticker barcode")).toBeInTheDocument();
  });
});

describe("CAT-3 Multiple copies", () => {
  test("Existing sticker: the entered barcode is sent with the copy", async () => {
    const calls = server(
      {
        "POST /api/catalog/lookup": { body: found },
        "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
        "POST /api/books/31/copies": { status: 201, body: { id: 70, book_id: 31, number: 1, condition: "worn", barcode: "0004417" } },
      },
      "own",
    );
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    await userEvent.click(within(copyCard(1)).getByRole("radio", { name: "Use a sticker" }));
    await userEvent.type(screen.getByLabelText("Sticker barcode"), "0004417");
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Condition"), "worn");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await screen.findByText(/0004417/)).toBeInTheDocument();
    expect(posted(calls, "/api/books/31/copies")[0]?.body).toEqual({ location_id: 1, condition: "worn", sticker: "0004417" });
  });

  test("Sticker equals a card number: the refusal says it is a card number, under that copy's sticker", async () => {
    server(
      {
        "POST /api/catalog/lookup": { body: found },
        "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
        "POST /api/books/31/copies": apiError(
          409,
          "conflict",
          "That code is a library card number, so it can't be a copy's barcode. Use a different barcode.",
          "sticker",
        ),
      },
      "own",
    );
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    await userEvent.click(within(copyCard(1)).getByRole("radio", { name: "Use a sticker" }));
    await userEvent.type(screen.getByLabelText("Sticker barcode"), "123456789012");
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await within(copyCard(1)).findByText(/is a library card number/)).toBeInTheDocument();
    expect(screen.queryByText(/is in the catalog/)).toBeNull();
  });

  test("a copy that fails after the book was saved is retried without making the book twice", async () => {
    let attempts = 0;
    const calls = server({
      "POST /api/catalog/lookup": { body: found },
      "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
      "POST /api/books/31/copies": () => {
        attempts++;
        return attempts === 1
          ? apiError(409, "conflict", "That code is already on another copy.", "sticker")
          : { status: 201, body: { id: 70, book_id: 31, number: 1, condition: "good", barcode: "C000070" } };
      },
    }, "own");
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    await userEvent.click(within(copyCard(1)).getByRole("radio", { name: "Use a sticker" }));
    await userEvent.type(screen.getByLabelText("Sticker barcode"), "X1");
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    expect(await screen.findByText("That code is already on another copy.")).toBeInTheDocument();
    expect(screen.getByLabelText("Title (required)")).toBeDisabled();
    expect(screen.getByText(/is saved, but not all its copies/)).toBeInTheDocument();
    expect(screen.queryByText(/Nothing is saved until you press Add/)).toBeNull();
    expect(screen.getByRole("link", { name: "the book's page" })).toHaveAttribute("href", "/books/31");
    await userEvent.click(within(copyCard(1)).getByRole("radio", { name: "New label" }));
    await userEvent.click(screen.getByRole("button", { name: "Save 1 copy" }));
    expect(await screen.findByText(/is in the catalog with 1 copy/)).toBeInTheDocument();
    expect(posted(calls, "/api/books")).toHaveLength(1);
  });

  test("two copies in a new location make it once", async () => {
    const calls = server({
      "POST /api/catalog/lookup": { body: found },
      "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
      "POST /api/locations": { status: 201, body: { id: 3, name: "Attic", copies: 0 } },
      "POST /api/books/31/copies": (b) => ({ status: 201, body: { id: 70 + (b as { location_id: number }).location_id, book_id: 31, number: 1, condition: "good", barcode: "" } }),
    });
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    await userEvent.click(screen.getByRole("button", { name: "One more" }));
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "new");
    await userEvent.type(within(copyCard(1)).getByLabelText("New location's name"), "Attic");
    // the second copy starts like the first
    expect(within(copyCard(2)).getByLabelText("Location")).toHaveValue("");
    await userEvent.selectOptions(within(copyCard(2)).getByLabelText("Location"), "new");
    await userEvent.type(within(copyCard(2)).getByLabelText("New location's name"), "attic");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 2 copies" }));
    await screen.findByText(/is in the catalog with 2 copies/);
    expect(posted(calls, "/api/locations")).toHaveLength(1);
    expect(posted(calls, "/api/books/31/copies").map((c) => (c.body as { location_id: number }).location_id)).toEqual([3, 3]);
  });
});

test("a copy in a location made in the same save shows that location on the saved screen", async () => {
  server({
    "POST /api/catalog/lookup": { body: found },
    "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
    "POST /api/locations": { status: 201, body: { id: 3, name: "Attic", copies: 0 } },
    "POST /api/books/31/copies": { status: 201, body: { id: 70, book_id: 31, number: 1, condition: "good", barcode: "" } },
  });
  await lookupWith(ISBN);
  await screen.findByText("Found on Open Library");
  await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "new");
  await userEvent.type(within(copyCard(1)).getByLabelText("New location's name"), "Attic");
  await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
  await screen.findByText(/is in the catalog/);
  expect(screen.getByText(/Attic/)).toBeInTheDocument();
});

test("a book without an author is refused before anything is sent", async () => {
  const calls = server({ "POST /api/catalog/lookup": { body: { status: "not_found", isbn: ISBN } } });
  await lookupWith(ISBN);
  await userEvent.type(await screen.findByLabelText("Title (required)"), "X");
  await userEvent.type(screen.getByLabelText("Authors (required)"), " , ");
  await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
  await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
  expect(screen.getByLabelText("Authors (required)")).toHaveAccessibleDescription(/A book needs at least one author\./);
  expect(posted(calls, "/api/books")).toHaveLength(0);
});

describe("Printing labels after saving", () => {
  test("the new copies' labels are asked for as a PDF in the chosen layout", async () => {
    const open = vi.fn(() => ({}));
    vi.stubGlobal("open", open);
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: () => "blob:labels", revokeObjectURL: () => {} }));
    const calls = server({
      "POST /api/catalog/lookup": { body: found },
      "POST /api/books": { status: 201, body: bookOf(31, { copies: [] }) },
      "POST /api/books/31/copies": { status: 201, body: { id: 70, book_id: 31, number: 1, condition: "good", barcode: "" } },
      "POST /api/catalog/labels": { body: {} },
    });
    await lookupWith(ISBN);
    await screen.findByText("Found on Open Library");
    await userEvent.selectOptions(within(copyCard(1)).getByLabelText("Location"), "1");
    await userEvent.click(screen.getByRole("button", { name: "Add book & 1 copy" }));
    await screen.findByText(/is in the catalog/);
    await userEvent.selectOptions(screen.getByLabelText("Label sheet"), "single");
    await userEvent.click(screen.getByRole("button", { name: "Print label" }));
    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(posted(calls, "/api/catalog/labels")[0]?.body).toEqual({ copy_ids: [70], layout: "single" });
  });
});

test("the /catalog/new route opens the screen for a librarian", async () => {
  server();
  await renderApp("/catalog/new");
  expect(await screen.findByRole("heading", { level: 1, name: "Add a book" })).toBeInTheDocument();
  expect(screen.getByLabelText("ISBN")).toBeInTheDocument();
});
