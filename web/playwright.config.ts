import { defineConfig, devices } from "@playwright/test";

// End-to-end tests against the real binary in web mode (see e2e/global-setup.ts).
// Run `pnpm build` first: the binary embeds the web build.
export const E2E_PORT = Number(process.env.TOLARIX_E2E_PORT ?? 18765);

export default defineConfig({
  testDir: "e2e",
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  timeout: 60_000,
  use: {
    baseURL: `http://127.0.0.1:${E2E_PORT}`,
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
