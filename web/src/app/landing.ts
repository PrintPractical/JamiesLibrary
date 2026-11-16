import { isStaff } from "./navigation";

/** Where a signed-in user starts: staff at the desk, patrons at home. */
export function homeFor(canAny: (permissions: readonly string[]) => boolean): string {
  return isStaff(canAny) ? "/desk" : "/";
}
