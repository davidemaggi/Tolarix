// Builds the tolarix binary into ./bin with version info. Cross-platform
// replacement for a Makefile. Run `pnpm build` to build the web UI first.
//   TOLARIX_VERSION=1.0.0 GOOS=linux GOARCH=arm64 node scripts/build-core.mjs
import { execFileSync } from "node:child_process";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const git = (...args) => {
  try {
    return execFileSync("git", args, { cwd: root, encoding: "utf8" }).trim();
  } catch {
    return "";
  }
};

const version = process.env.TOLARIX_VERSION || git("describe", "--tags", "--always", "--dirty") || "dev";
const commit = git("rev-parse", "HEAD");
const date = new Date().toISOString().replace(/\.\d+Z$/, "Z");
const goos = process.env.GOOS || process.platform.replace("win32", "windows");
const ext = goos === "windows" ? ".exe" : "";
const out = path.join(root, "bin", `tolarix${ext}`);
const pkg = "github.com/davidemaggi/tolarix/core/internal/buildinfo";

execFileSync(
  "go",
  [
    "build",
    "-trimpath",
    "-ldflags",
    `-s -w -X ${pkg}.Version=${version} -X ${pkg}.Commit=${commit} -X ${pkg}.Date=${date}`,
    "-o",
    out,
    "./cmd/tolarix",
  ],
  { cwd: path.join(root, "core"), stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } },
);
console.log(`built ${path.relative(root, out)} (${version})`);
