// Builds the release archives of the tolarix CLI/server into ./dist:
// one archive per platform (tar.gz, zip on Windows) with the binary, the
// README and the LICENSE, plus SHA256SUMS. Build the web UI first.
//   TOLARIX_VERSION=1.0.0 node scripts/package-cli.mjs
// Needs `tar` and, for the Windows archives, `zip`.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..");
const version = process.env.TOLARIX_VERSION;
if (!version) {
  console.error("TOLARIX_VERSION is required (e.g. 1.0.0)");
  process.exit(1);
}
if (!fs.existsSync(path.join(root, "core/internal/webui/dist/index.html"))) {
  console.error("The web UI is not built: run `pnpm --filter web build` first.");
  process.exit(1);
}

const targets = [
  ["linux", "amd64"],
  ["linux", "arm64"],
  ["darwin", "amd64"],
  ["darwin", "arm64"],
  ["windows", "amd64"],
  ["windows", "arm64"],
];

const dist = path.join(root, "dist");
fs.rmSync(dist, { recursive: true, force: true });
fs.mkdirSync(dist);

const archives = [];
for (const [goos, goarch] of targets) {
  const name = `tolarix_${version}_${goos}_${goarch}`;
  const stage = path.join(dist, name);
  const exe = goos === "windows" ? "tolarix.exe" : "tolarix";
  fs.mkdirSync(stage);
  execFileSync("node", [path.join(root, "scripts/build-core.mjs")], {
    stdio: "inherit",
    env: { ...process.env, GOOS: goos, GOARCH: goarch, TOLARIX_OUT: path.join(stage, exe) },
  });
  for (const f of ["README.md", "LICENSE"]) fs.copyFileSync(path.join(root, f), path.join(stage, f));

  const archive = goos === "windows" ? `${name}.zip` : `${name}.tar.gz`;
  if (goos === "windows") {
    execFileSync("zip", ["-qr", archive, name], { cwd: dist, stdio: "inherit" });
  } else {
    execFileSync("tar", ["-czf", archive, name], { cwd: dist, stdio: "inherit" });
  }
  fs.rmSync(stage, { recursive: true });
  archives.push(archive);
}

const sums = archives.map((a) => {
  const hash = createHash("sha256").update(fs.readFileSync(path.join(dist, a))).digest("hex");
  return `${hash}  ${a}`;
});
fs.writeFileSync(path.join(dist, "SHA256SUMS"), sums.join("\n") + "\n");
console.log(`packaged ${archives.length} archives in dist/`);
