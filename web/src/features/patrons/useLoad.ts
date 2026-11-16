import { useCallback, useEffect, useRef, useState } from "react";

export type Loaded<T> = { status: "loading" } | { status: "error"; error: unknown } | { status: "ready"; data: T };

type Settled<T> = { key: unknown; tick: number; result: { error: unknown } | { data: T } };

/**
 * Runs `load` when `key` changes, and again on `reload`. A reload keeps showing what was there
 * until the new answer comes; a new key shows "loading". An answer for a page that has moved on is dropped.
 */
export function useLoad<T>(load: (signal: AbortSignal) => Promise<T>, key: unknown) {
  const [settled, setSettled] = useState<Settled<T> | null>(null);
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((t) => t + 1), []);
  const loadRef = useRef(load);
  useEffect(() => {
    loadRef.current = load;
  });
  useEffect(() => {
    const ctl = new AbortController();
    loadRef.current(ctl.signal).then(
      (data) => setSettled({ key, tick, result: { data } }),
      (error) => {
        if (!ctl.signal.aborted) setSettled({ key, tick, result: { error } });
      },
    );
    return () => ctl.abort();
  }, [key, tick]);

  let state: Loaded<T> = { status: "loading" };
  if (settled && Object.is(settled.key, key)) {
    state = "error" in settled.result ? { status: "error", error: settled.result.error } : { status: "ready", data: settled.result.data };
  }
  return { state, reload };
}
