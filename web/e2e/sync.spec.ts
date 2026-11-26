import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";

import { expect, test, type Page } from "@playwright/test";

const NEW_PASSWORD = "correct horse battery staple";

function markdownFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true, recursive: true })
    .filter((e) => e.isFile() && e.name.endsWith(".md"))
    .map((e) => path.join(e.parentPath, e.name));
}

async function pick(page: Page, combobox: string, option: string) {
  await page.getByRole("combobox", { name: combobox }).click();
  await page.getByRole("option", { name: option }).click();
}

async function addConnection(page: Page, name: string, vault: string) {
  await page
    .getByRole("navigation", { name: "Main navigation" })
    .getByRole("link", { name: "Connections" })
    .first()
    .click();
  await page.getByRole("link", { name: "Add connection" }).first().click();
  await page.getByRole("radio", { name: /Obsidian/ }).click();
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByLabel("Vault folder", { exact: true }).fill(vault);
  await page.getByRole("button", { name: "Test connection" }).click();
  await expect(page.getByText("It works.")).toBeVisible();
  await page.getByRole("button", { name: "Add connection" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Connections" })).toBeVisible();
  await expect(page.getByRole("cell", { name, exact: true })).toBeVisible();
}

test("first start: change password, connect two vaults, create a flow and run it", async ({
  page,
}) => {
  const master = process.env.E2E_MASTER!;
  const mirror = process.env.E2E_MIRROR!;

  // Sign in with the one-time password, then choose a real one.
  await page.goto("/");
  await page.getByLabel("Password", { exact: true }).fill(process.env.E2E_PASSWORD!);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Choose your password" })).toBeVisible();
  await page.getByLabel("Current password", { exact: true }).fill(process.env.E2E_PASSWORD!);
  await page.getByLabel("New password", { exact: true }).fill(NEW_PASSWORD);
  await page.getByLabel("Repeat the new password", { exact: true }).fill(NEW_PASSWORD);
  await page.getByRole("button", { name: "Save and continue" }).click();
  await expect(page.getByRole("heading", { name: "No flows yet" })).toBeVisible();

  await addConnection(page, "Team vault", master);
  await addConnection(page, "Public vault", mirror);

  // Flow wizard.
  await page
    .getByRole("navigation", { name: "Main navigation" })
    .getByRole("link", { name: "Flows" })
    .first()
    .click();
  await page.getByRole("link", { name: "New flow" }).first().click();
  const next = page.getByRole("button", { name: "Next" });
  await page.getByLabel("Name", { exact: true }).fill("Handbook");
  await pick(page, "Master wiki", "Team vault");
  await next.click();
  await expect(page.getByRole("checkbox", { name: "Guides" })).toBeVisible();
  await next.click();
  await pick(page, "Wiki", "Public vault");
  await next.click();
  await next.click();

  // Schedule: weekdays at 07:30 (the default time), explained by the server.
  await page.getByRole("radio", { name: "On these weekdays at…" }).click();
  const preview = page.getByRole("region", { name: "Preview" });
  await expect(preview.getByText("Every weekday at 07:30, Europe/Rome")).toBeVisible();
  await expect(preview.getByText("30 7 * * 1-5")).toBeVisible();
  await expect(preview.getByRole("listitem")).toHaveCount(5);
  // The advanced tab shows the same schedule as a cron expression.
  await page.getByRole("tab", { name: "Advanced (cron expression)" }).click();
  await expect(page.getByRole("textbox", { name: "Cron expression" })).toHaveValue("30 7 * * 1-5");
  await page.getByRole("tab", { name: "Simple" }).click();
  // Leave it to "tolarix run-due", so the server never starts it during the test.
  await page.getByRole("switch", { name: "Run with the built-in scheduler" }).click();
  await next.click();
  await expect(page.getByText("Every weekday at 07:30, Europe/Rome")).toBeVisible();
  await page.getByRole("button", { name: "Create flow" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Handbook" })).toBeVisible();
  await expect(page.getByText("Every weekday at 07:30, Europe/Rome")).toBeVisible();
  await expect(page.getByText('Runs only via "tolarix run-due"')).toBeVisible();

  // A dry-run changes nothing.
  await page.getByRole("button", { name: "Dry-run Handbook" }).click();
  await expect(page.getByText("Files uploaded")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText(/This was a dry-run/)).toBeVisible();
  expect(markdownFiles(mirror)).toHaveLength(0);

  // A real run copies the pages and shows them in the report.
  await page.getByRole("link", { name: "Handbook" }).click();
  await page.getByRole("button", { name: "Run Handbook now" }).click();
  await expect(page.getByText("Files uploaded")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Succeeded", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Report" }).click();
  await expect(page.getByRole("cell", { name: "Welcome", exact: true })).toBeVisible();

  const copied = markdownFiles(mirror).map((f) => readFileSync(f, "utf8"));
  expect(copied.some((s) => s.includes("Start here."))).toBe(true);
  expect(copied.some((s) => s.includes("Run the binary."))).toBe(true);

  // The run is in the history.
  await page
    .getByRole("navigation", { name: "Main navigation" })
    .getByRole("link", { name: "Runs" })
    .first()
    .click();
  await expect(page.getByRole("cell", { name: "Handbook", exact: true }).first()).toBeVisible();

  // Signing out asks for the new password again.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});
