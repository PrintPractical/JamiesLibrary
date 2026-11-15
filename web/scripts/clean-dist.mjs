// Empties internal/webui/dist before a build, keeping the .gitkeep that lets
// `go build` work on a checkout that has never built the web app.
import { mkdirSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const dist = fileURLToPath(new URL("../../internal/webui/dist", import.meta.url));
mkdirSync(dist, { recursive: true });
for (const name of readdirSync(dist)) {
  if (name !== ".gitkeep") rmSync(join(dist, name), { recursive: true, force: true });
}
writeFileSync(join(dist, ".gitkeep"), "");
