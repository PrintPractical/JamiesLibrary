// The one way the web app talks to /api. Errors come out typed: an ApiError for
// anything the server answered with a failure, a NetworkError when it never
// answered (architecture §13).

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field: string | undefined;
  readonly requestId: string | undefined;

  constructor(init: { status: number; code: string; message: string; field?: string; requestId?: string }) {
    super(init.message);
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.field = init.field;
    this.requestId = init.requestId;
  }
}

export class NetworkError extends Error {
  constructor(options?: ErrorOptions) {
    super("Can't reach the library. Check your connection and try again.", options);
    this.name = "NetworkError";
  }
}

export type ApiOptions = {
  method?: string;
  /** Sent as JSON. */
  body?: unknown;
  signal?: AbortSignal;
};

/** Turns a failed response into an ApiError, whatever the body is. */
async function toApiError(res: Response): Promise<ApiError> {
  let detail: unknown;
  try {
    detail = ((await res.json()) as { error?: unknown } | null)?.error;
  } catch {
    detail = undefined;
  }
  const d = (typeof detail === "object" && detail !== null ? detail : {}) as Record<string, unknown>;
  const str = (v: unknown) => (typeof v === "string" && v !== "" ? v : undefined);
  const code = str(d.code);
  const message = str(d.message);
  if (code && message) {
    return new ApiError({ status: res.status, code, message, field: str(d.field), requestId: str(d.request_id) });
  }
  return new ApiError({
    status: res.status,
    code: "unexpected_response",
    message: `The server sent a reply the app couldn't read (status ${res.status}). Try again in a moment.`,
  });
}

/** Calls the API and returns the parsed JSON body (undefined for 204). Throws ApiError or NetworkError. */
export async function api<T = undefined>(path: string, options: ApiOptions = {}): Promise<T> {
  const { method = "GET", body, signal } = options;
  const headers: Record<string, string> = { Accept: "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    });
  } catch (err) {
    if (signal?.aborted) throw err; // a caller's own cancel is not a network fault
    throw new NetworkError({ cause: err });
  }

  if (!res.ok) throw await toApiError(res);
  if (res.status === 204) return undefined as T;
  try {
    return (await res.json()) as T;
  } catch {
    throw new ApiError({
      status: res.status,
      code: "unexpected_response",
      message: "The server sent a reply the app couldn't read. Try again in a moment.",
    });
  }
}

/** A sentence to show a user for any error thrown by `api`. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError || err instanceof NetworkError) return err.message;
  return "Something went wrong. Try again.";
}
