import { defineConfig } from "@playwright/test";

// Screenshots for the README and the manuals (docs/images/<lang>/).
// Run `pnpm screenshots` from the repository root: it builds the binary and
// runs this once per UI language.
export const SHOTS_PORT = Number(process.env.TOLARIX_SHOTS_PORT ?? 18766);
const lang = process.env.SHOTS_LANG ?? "en";

export default defineConfig({
  testDir: ".",
  testMatch: "tour.spec.ts",
  globalSetup: "./setup.ts",
  workers: 1,
  timeout: 180_000,
  reporter: "list",
  use: {
    baseURL: `http://127.0.0.1:${SHOTS_PORT}`,
    browserName: "chromium",
    viewport: { width: 1280, height: 800 },
    locale: lang === "it" ? "it-IT" : "en-GB",
    timezoneId: "Europe/Rome",
    colorScheme: "light",
  },
});
