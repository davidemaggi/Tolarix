// Builds the tolarix binary into ./bin with version info. Cross-platform
// replacement for a Makefile. Run `pnpm build` to build the web UI first.
//   TOLARIX_VERSION=1.0.0 GOOS=linux GOARCH=arm64 node scripts/build-core.mjs
// TOLARIX_OUT overrides the output file (the desktop sidecar uses it).
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
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
const out = process.env.TOLARIX_OUT
  ? path.resolve(process.env.TOLARIX_OUT)
  : path.join(root, "bin", `tolarix${ext}`);
const pkg = "github.com/davidemaggi/tolarix/core/internal/buildinfo";

// findGo returns the go command: from PATH, else from GOROOT or the default
// install folder (a terminal opened before installing Go has a stale PATH).
function findGo() {
  try {
    execFileSync("go", ["version"], { stdio: "ignore" });
    return "go";
  } catch {
    // not on PATH
  }
  const exe = process.platform === "win32" ? "go.exe" : "go";
  const candidates = [
    process.env.GOROOT && path.join(process.env.GOROOT, "bin", exe),
    process.platform === "win32" && path.join(process.env.ProgramFiles || "C:\\Program Files", "Go", "bin", exe),
    process.platform !== "win32" && "/usr/local/go/bin/go",
  ].filter(Boolean);
  const found = candidates.find((c) => existsSync(c));
  if (found) return found;
  console.error(
    "Go was not found. Building the tolarix binary needs Go (the version in core/go.mod or newer):\n" +
      "  1. install it from https://go.dev/dl/\n" +
      "  2. open a new terminal, so the updated PATH is picked up, and check with `go version`\n" +
      "To try Tolarix without a Go toolchain, use the Docker image instead (see docs/admin-guide.md).",
  );
  process.exit(1);
}

execFileSync(
  findGo(),
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
