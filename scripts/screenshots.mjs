// Regenerates the screenshots in docs/images/<lang>/ used by the README and
// the manuals: builds the web UI and the binary, then walks through the app
// once per UI language (web/screenshots/tour.spec.ts).
//   pnpm screenshots            # every language
//   pnpm screenshots it         # one language
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const langs = process.argv.slice(2).length
  ? process.argv.slice(2)
  : ["en", "it"];
const bin = path.join(
  root,
  "bin",
  `tolarix${process.platform === "win32" ? ".exe" : ""}`,
);
const run = (cmd, args, opts = {}) =>
  execFileSync(cmd, args, {
    cwd: root,
    stdio: "inherit",
    shell: process.platform === "win32",
    ...opts,
  });

run("pnpm", ["--filter", "web", "build"]);
// The footer shows the latest release instead of the git description.
run("node", ["scripts/build-core.mjs"], {
  env: {
    ...process.env,
    TOLARIX_VERSION: process.env.TOLARIX_VERSION || latestTag() || "dev",
  },
});
// The newest "## [X.Y.Z]" heading of the changelog.
function latestTag() {
  const m = /^## \[(\d+\.\d+\.\d+[^\]]*)\]/m.exec(
    readFileSync(path.join(root, "CHANGELOG.md"), "utf8"),
  );
  return m ? m[1] : "";
}

for (const lang of langs) {
  console.log(`\nscreenshots: ${lang}`);
  run(
    "pnpm",
    ["exec", "playwright", "test", "-c", "screenshots/playwright.config.ts"],
    {
      cwd: path.join(root, "web"),
      env: { ...process.env, SHOTS_LANG: lang, TOLARIX_BIN: bin },
    },
  );
}
