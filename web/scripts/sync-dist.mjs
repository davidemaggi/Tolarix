// Copies web/dist into core/internal/webui/dist, where the Go binary embeds it.
// The target keeps its .gitkeep so `go build` works on a fresh clone.
import { cpSync, mkdirSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";

const here = import.meta.dirname;
const src = path.resolve(here, "../dist");
const dst = path.resolve(here, "../../core/internal/webui/dist");

mkdirSync(dst, { recursive: true });
for (const entry of readdirSync(dst)) {
  if (entry !== ".gitkeep") rmSync(path.join(dst, entry), { recursive: true, force: true });
}
cpSync(src, dst, { recursive: true });
writeFileSync(path.join(dst, ".gitkeep"), "");
console.log(`web build copied to ${path.relative(process.cwd(), dst)}`);
