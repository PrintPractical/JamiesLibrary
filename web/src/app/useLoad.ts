import { useCallback, useEffect, useState } from "react";

type State<T> = { status: "loading" } | { status: "ready"; data: T } | { status: "error"; error: unknown };

/** Loads something when the page opens, and again when `load` changes or `reload` is called. */
export function useLoad<T>(load: (signal: AbortSignal) => Promise<T>) {
  const [state, setState] = useState<State<T>>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    load(ctl.signal).then(
      (data) => setState({ status: "ready", data }),
      (error: unknown) => {
        if (!ctl.signal.aborted) setState({ status: "error", error });
      },
    );
    return () => ctl.abort();
  }, [load, attempt]);

  // Not "loading" again: what is on the screen stays until the new answer is in.
  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  const retry = useCallback(() => {
    setState({ status: "loading" });
    setAttempt((n) => n + 1);
  }, []);
  const set = useCallback((data: T) => setState({ status: "ready", data }), []);
  return { state, reload, retry, set };
}
