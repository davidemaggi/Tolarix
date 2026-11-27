// Builds the tolarix server for the desktop app (Tauri's externalBin):
//   src-tauri/binaries/tolarix-<target triple>[.exe]
// Tauri runs this before `tauri dev` and `tauri build`. The target is
// TAURI_ENV_TARGET_TRIPLE (set by Tauri, e.g. for --target) or the Rust host.
// The embedded web UI is built first when it is missing (or always with
// TOLARIX_BUILD_WEB=1).
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import path from "node:path";

const desktop = path.resolve(import.meta.dirname, "..");
const root = path.resolve(desktop, "..");

function hostTriple() {
  const out = execFileSync("rustc", ["-vV"], { encoding: "utf8" });
  const m = /^host: (\S+)$/m.exec(out);
  if (!m) throw new Error("cannot read the Rust host triple from `rustc -vV`");
  return m[1];
}

/** GOOS and GOARCH for a Rust target triple. */
export function goTarget(triple) {
  const arch = {
    x86_64: "amd64",
    aarch64: "arm64",
    i686: "386",
    armv7: "arm",
  }[triple.split("-")[0]];
  const os = triple.includes("windows")
    ? "windows"
    : triple.includes("darwin")
      ? "darwin"
      : triple.includes("linux")
        ? "linux"
        : undefined;
  if (!arch || !os) throw new Error(`unsupported target ${triple}`);
  return { GOOS: os, GOARCH: arch, ...(arch === "arm" ? { GOARM: "7" } : {}) };
}

const triple = process.env.TAURI_ENV_TARGET_TRIPLE || hostTriple();
const target = goTarget(triple);
const ext = target.GOOS === "windows" ? ".exe" : "";
const out = path.join(desktop, "src-tauri", "binaries", `tolarix-${triple}${ext}`);
mkdirSync(path.dirname(out), { recursive: true });

const ui = path.join(root, "core", "internal", "webui", "dist", "index.html");
if (process.env.TOLARIX_BUILD_WEB === "1" || !existsSync(ui)) {
  execFileSync("pnpm", ["--filter", "web", "build"], {
    cwd: root,
    stdio: "inherit",
    shell: process.platform === "win32",
  });
}

execFileSync(process.execPath, [path.join(root, "scripts", "build-core.mjs")], {
  cwd: root,
  stdio: "inherit",
  env: { ...process.env, ...target, TOLARIX_OUT: out },
});
