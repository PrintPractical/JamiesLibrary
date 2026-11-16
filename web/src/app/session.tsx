import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError } from "../api/client";
import * as authApi from "../api/auth";
import type { Library, Me } from "../api/auth";
import { PageError } from "../components/Errors";
import { COLOUR_TOKENS, hearth, type Scheme } from "./theme";
import { ThemeProvider } from "./ThemeProvider";

type Loaded = { library: Library; me: Me | null };
type State = ({ status: "ready" } & Loaded) | { status: "loading" } | { status: "error"; error: unknown };

export type Session = Loaded & {
  /** Whether the signed-in user holds a permission. Display only: the server decides. */
  can: (permission: string) => boolean;
  /** Whether the user holds at least one of the permissions. */
  canAny: (permissions: readonly string[]) => boolean;
  /** Records the user the server just signed in, registered or returned from /api/me. */
  setMe: (me: Me) => void;
  /** Re-reads the library's name, logo and colours (after an administrator changes them). */
  reloadLibrary: () => Promise<void>;
  /** Re-reads who is signed in (after a password change, say). */
  refresh: () => Promise<void>;
  signOut: () => Promise<void>;
};

const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession needs a SessionProvider");
  return s;
}

const HEX = /^#[0-9a-fA-F]{6}$/;

/** The library's colours over Hearth: only known tokens with plain #RRGGBB values are taken. */
export function schemeOf(library: Library | undefined): Scheme {
  const scheme = { ...hearth };
  for (const token of COLOUR_TOKENS) {
    const value = library?.colours?.[`--${token}`];
    if (typeof value === "string" && HEX.test(value)) scheme[token] = value;
  }
  return scheme;
}

/** Drops what the app cached for the signed-in user (architecture: sign-out clears the app's caches). */
async function clearCaches() {
  if (typeof caches === "undefined") return;
  try {
    for (const key of await caches.keys()) await caches.delete(key);
  } catch {
    // Cache storage can be unavailable (private windows); there is then nothing of ours in it.
  }
}

async function load(signal: AbortSignal): Promise<Loaded> {
  const [library, me] = await Promise.all([
    authApi.getLibrary(signal),
    authApi.getMe(signal).catch((err: unknown) => {
      if (err instanceof ApiError && err.status === 401) return null;
      throw err;
    }),
  ]);
  return { library, me };
}

/** Holds who is signed in (GET /api/me) and the library's branding (GET /api/library), and themes the app with it. */
export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal).then(
      (loaded) => setState({ status: "ready", ...loaded }),
      (error: unknown) => {
        if (!ctl.signal.aborted) setState({ status: "error", error });
      },
    );
    return () => ctl.abort();
  }, [attempt]);

  const setMe = useCallback((me: Me | null) => {
    setState((s) => (s.status === "ready" ? { ...s, me } : s));
  }, []);

  const refresh = useCallback(async () => {
    const me = await authApi.getMe().catch((err: unknown) => {
      if (err instanceof ApiError && err.status === 401) return null;
      throw err;
    });
    setMe(me);
  }, [setMe]);

  const reloadLibrary = useCallback(async () => {
    const library = await authApi.getLibrary();
    setState((s) => (s.status === "ready" ? { ...s, library } : s));
  }, []);

  const signOut = useCallback(async () => {
    try {
      await authApi.signOut();
    } catch (err) {
      // Already signed out is what was asked for; anything else leaves the user signed in.
      if (!(err instanceof ApiError && err.status === 401)) throw err;
    }
    await clearCaches();
    setMe(null);
  }, [setMe]);

  const value = useMemo<Session | null>(() => {
    if (state.status !== "ready") return null;
    const held = new Set(state.me?.permissions ?? []);
    return {
      library: state.library,
      me: state.me,
      can: (p) => held.has(p),
      canAny: (ps) => ps.some((p) => held.has(p)),
      setMe,
      reloadLibrary,
      refresh,
      signOut,
    };
  }, [state, setMe, reloadLibrary, refresh, signOut]);

  const library = state.status === "ready" ? state.library : undefined;
  return (
    <ThemeProvider scheme={schemeOf(library)}>
      {state.status === "error" ? (
        <div className="content">
          <PageError error={state.error} onRetry={() => (setState({ status: "loading" }), setAttempt((n) => n + 1))} />
        </div>
      ) : value ? (
        <SessionContext.Provider value={value}>{children}</SessionContext.Provider>
      ) : (
        <p className="content muted" role="status">
          Loading…
        </p>
      )}
    </ThemeProvider>
  );
}
