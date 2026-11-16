import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// Security design §8: user and provider text is rendered as React text, never as markup.
function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const path = join(dir, e.name);
    if (e.isDirectory()) return sources(path);
    return /\.(ts|tsx)$/.test(e.name) && !/\.test\.tsx?$/.test(e.name) ? [path] : [];
  });
}

test("no source renders HTML from a string", () => {
  const offenders = sources(join(process.cwd(), "src")).filter((f) =>
    /dangerouslySetInnerHTML|\.innerHTML|\.outerHTML|insertAdjacentHTML/.test(readFileSync(f, "utf8")),
  );
  expect(offenders).toEqual([]);
});
