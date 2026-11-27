import { spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

import { SHOTS_PORT } from "./playwright.config";

// A 1×1 transparent PNG, so the sample attachment is a real image.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

const PAGES: Record<string, string> = {
  "Welcome.md":
    "# Welcome\n\nThis is the engineering handbook. New here? Start with [[First week]].\n",
  "Glossary.md": "# Glossary\n\n| Term | Meaning |\n|---|---|\n| SLO | Service level objective |\n",
  "Onboarding/First week.md":
    "# First week\n\n- [ ] Get your [[Accounts and access]]\n- [ ] Read the [[Overview|architecture overview]]\n",
  "Onboarding/Accounts and access.md":
    "# Accounts and access\n\nAsk the service desk for a laptop.\n",
  "Architecture/Overview.md":
    "# Overview\n\n```mermaid\ngraph LR\n  Web --> API --> Billing\n  API --> Search\n```\n\n![[context.png|400]]\n",
  "Architecture/context.png": "",
  "Architecture/Services/Billing.md": "# Billing\n\nInvoices are created nightly.\n",
  "Architecture/Services/Search.md": "# Search\n\nBacked by OpenSearch.\n",
  "Runbooks/Deploy.md":
    "# Deploy\n\n> [!warning] Freeze\n> No deploys on Fridays.\n\nSee also [[Ideas]] for what comes next.\n",
  "Runbooks/Incident response.md":
    "# Incident response\n\n1. Page the on-call\n2. Open a channel\n",
  "Drafts/Ideas.md": "# Ideas\n\nNot ready to share.\n",
};

/**
 * Starts `tolarix serve` on a fresh data directory with a sample handbook
 * vault, an empty mirror vault and two export folders. The binary comes
 * from TOLARIX_BIN (pnpm screenshots builds it).
 */
export default async function setup() {
  // A short, fixed path where possible: it shows in the screenshots.
  let root: string;
  if (process.platform === "win32") {
    root = mkdtempSync(path.join(tmpdir(), "tolarix-demo-"));
  } else {
    root = "/tmp/tolarix-demo";
    rmSync(root, { recursive: true, force: true });
    mkdirSync(root);
  }
  const dirs = {
    master: path.join(root, "vaults", "handbook"),
    mirror: path.join(root, "vaults", "public"),
    site: path.join(root, "exports", "docs-site"),
    github: path.join(root, "exports", "github-wiki"),
  };
  for (const d of Object.values(dirs)) mkdirSync(d, { recursive: true });
  for (const [rel, body] of Object.entries(PAGES)) {
    const p = path.join(dirs.master, rel);
    mkdirSync(path.dirname(p), { recursive: true });
    writeFileSync(p, rel.endsWith(".png") ? PNG : body);
  }

  const bin = process.env.TOLARIX_BIN;
  if (!bin)
    throw new Error("TOLARIX_BIN is not set: run `pnpm screenshots` from the repository root.");
  const server = spawn(
    bin,
    [
      "serve",
      "--data-dir",
      path.join(root, "data"),
      "--host",
      "127.0.0.1",
      "--port",
      String(SHOTS_PORT),
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

  process.env.SHOTS_PASSWORD = password;
  process.env.SHOTS_MASTER = dirs.master;
  process.env.SHOTS_MIRROR = dirs.mirror;
  process.env.SHOTS_SITE = dirs.site;
  process.env.SHOTS_GITHUB = dirs.github;

  return async () => {
    server.kill("SIGTERM");
    await new Promise((r) => server.once("exit", r));
    rmSync(root, { recursive: true, force: true });
  };
}
