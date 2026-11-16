import { useCallback, type ReactNode } from "react";
import { Navigate } from "react-router";
import { setupStatus } from "../../api/setup";
import { useLoad } from "../../app/useLoad";
import { useSession } from "../../app/session";

/**
 * Wraps the pages a visitor lands on (sign in, register). While the server says
 * first-time setup is open there is no one to sign in as and no invitation to
 * give, so the visitor is sent to /setup. Once it is closed, or when the answer
 * can't be had, the page shows as it always did.
 */
export function SetupGate({ children }: { children: ReactNode }) {
  const { me } = useSession();
  if (me) return children;
  return <Check>{children}</Check>;
}

function Check({ children }: { children: ReactNode }) {
  const load = useCallback((signal: AbortSignal) => setupStatus(signal), []);
  const { state } = useLoad(load);
  if (state.status === "loading") {
    return (
      <p className="content muted" role="status">
        Loading…
      </p>
    );
  }
  if (state.status === "ready" && state.data.open) return <Navigate to="/setup" replace />;
  return children;
}
