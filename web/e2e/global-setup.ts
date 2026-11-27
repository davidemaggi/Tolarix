import { execFileSync, spawn } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

import { E2E_PORT } from "../playwright.config";

const core = path.resolve(import.meta.dirname, "../../core");

/**
 * Start `tolarix serve` in web mode on a fresh data directory with two
 * Obsidian vaults, and hand the one-time admin password to the tests.
 * TOLARIX_BIN skips the build (CI passes the binary it already built).
 */
export default async function globalSetup() {
  const root = mkdtempSync(path.join(tmpdir(), "tolarix-e2e-"));
  const master = path.join(root, "vaults", "master");
  const mirror = path.join(root, "vaults", "mirror");
  mkdirSync(path.join(master, "Guides"), { recursive: true });
  mkdirSync(mirror, { recursive: true });
  writeFileSync(path.join(master, "Welcome.md"), "# Welcome\n\nStart here.\n");
  writeFileSync(path.join(master, "Guides", "Install.md"), "# Install\n\nRun the binary.\n");

  let bin = process.env.TOLARIX_BIN;
  if (!bin) {
    if (!existsSync(path.join(core, "internal/webui/dist/index.html"))) {
      throw new Error("No web build to embed: run `pnpm build` in web/ first.");
    }
    bin = path.join(root, process.platform === "win32" ? "tolarix.exe" : "tolarix");
    execFileSync("go", ["build", "-o", bin, "./cmd/tolarix"], { cwd: core, stdio: "inherit" });
  }

  const server = spawn(
    bin,
    [
      "serve",
      "--data-dir",
      path.join(root, "data"),
      "--host",
      "127.0.0.1",
      "--port",
      String(E2E_PORT),
    ],
    { env: { ...process.env, TOLARIX_MODE: "web", TOLARIX_TIMEZONE: "Europe/Rome" } },
  );
  let log = "";
  const password = await new Promise<string>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`server did not start:\n${log}`)), 30_000);
    const onData = (chunk: Buffer) => {
      log += chunk.toString();
      const m = /password: (\S+)/.exec(log);
      if (m) {
        clearTimeout(timer);
        resolve(m[1]!);
      }
    };
    server.stdout.on("data", onData);
    server.stderr.on("data", onData);
    server.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`server exited with ${code}:\n${log}`));
    });
  });
  await waitHealthy(`http://127.0.0.1:${E2E_PORT}/healthz`);

  process.env.E2E_PASSWORD = password;
  process.env.E2E_MASTER = master;
  process.env.E2E_MIRROR = mirror;

  return async () => {
    server.kill("SIGTERM");
    await new Promise((r) => server.once("exit", r));
    rmSync(root, { recursive: true, force: true });
  };
}

async function waitHealthy(url: string) {
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`${url} never became healthy`);
}
