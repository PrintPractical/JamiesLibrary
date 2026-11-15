import { afterEach, vi } from "vitest";
import { api, ApiError, errorMessage, NetworkError } from "./client";

afterEach(() => vi.unstubAllGlobals());

function stubFetch(impl: (url: string, init: RequestInit) => Promise<Response>) {
  const fn = vi.fn(impl);
  vi.stubGlobal("fetch", fn);
  return fn;
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

test("sends JSON with same-origin credentials and returns the parsed body", async () => {
  const fetch = stubFetch(async () => json(200, { name: "x" }));
  const got = await api<{ name: string }>("/api/things", { method: "POST", body: { a: 1 } });
  expect(got).toEqual({ name: "x" });
  const [url, init] = fetch.mock.calls[0]!;
  expect(url).toBe("/api/things");
  expect(init.method).toBe("POST");
  expect(init.credentials).toBe("same-origin");
  expect(init.body).toBe('{"a":1}');
  expect(init.headers).toMatchObject({ "Content-Type": "application/json" });
});

test("a GET sends no body and no content type", async () => {
  const fetch = stubFetch(async () => json(200, {}));
  await api("/api/me");
  const init = fetch.mock.calls[0]![1];
  expect(init.method).toBe("GET");
  expect(init.body).toBeUndefined();
  expect(init.headers).not.toHaveProperty("Content-Type");
});

test("a 204 gives undefined", async () => {
  stubFetch(async () => new Response(null, { status: 204 }));
  await expect(api("/api/sign-out", { method: "POST" })).resolves.toBeUndefined();
});

test("parses the server's JSON error into an ApiError", async () => {
  stubFetch(async () =>
    json(409, { error: { code: "conflict", message: "This copy is already on loan.", field: "code" } }),
  );
  const err = await api("/api/x").catch((e: unknown) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err).toMatchObject({
    status: 409,
    code: "conflict",
    message: "This copy is already on loan.",
    field: "code",
  });
});

test("keeps the request ID of a server fault", async () => {
  stubFetch(async () =>
    json(500, { error: { code: "internal", message: "Quote the request ID abc.", request_id: "abc" } }),
  );
  await expect(api("/api/x")).rejects.toMatchObject({ status: 500, code: "internal", requestId: "abc" });
});

test("a non-JSON 500 becomes an ApiError with a sentence a user can act on", async () => {
  stubFetch(async () => new Response("<html>Bad gateway</html>", { status: 500 }));
  const err = await api("/api/x").catch((e: unknown) => e);
  expect(err).toBeInstanceOf(ApiError);
  expect(err).toMatchObject({ status: 500, code: "unexpected_response", field: undefined });
  expect((err as ApiError).message).not.toContain("<html>");
  expect((err as ApiError).message).toContain("500");
});

test("JSON of the wrong shape on a failure is treated like no JSON", async () => {
  stubFetch(async () => json(502, { error: "nope" }));
  await expect(api("/api/x")).rejects.toMatchObject({ status: 502, code: "unexpected_response" });
});

test("a failed network call is a NetworkError, not an ApiError", async () => {
  stubFetch(async () => {
    throw new TypeError("Failed to fetch");
  });
  const err = await api("/api/x").catch((e: unknown) => e);
  expect(err).toBeInstanceOf(NetworkError);
  expect(err).not.toBeInstanceOf(ApiError);
  expect(errorMessage(err)).toMatch(/connection/i);
});

test("a caller's own abort is passed on as it is", async () => {
  const ctl = new AbortController();
  stubFetch(async () => {
    ctl.abort();
    throw new DOMException("aborted", "AbortError");
  });
  const err = await api("/api/x", { signal: ctl.signal }).catch((e: unknown) => e);
  expect(err).not.toBeInstanceOf(NetworkError);
  expect((err as DOMException).name).toBe("AbortError");
});

test("a success with an unreadable body is an ApiError", async () => {
  stubFetch(async () => new Response("not json", { status: 200 }));
  await expect(api("/api/x")).rejects.toMatchObject({ code: "unexpected_response" });
});
