import { firstScreen } from "./navigation";

/** Where a signed-in user starts: staff at their first screen, patrons at home. */
export function homeFor(canAny: (permissions: readonly string[]) => boolean): string {
  return firstScreen(canAny) ?? "/";
}
