import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

import { expect, test, type Page } from "@playwright/test";

// Walks through the app like a new user and saves a screenshot at each
// step into docs/images/<lang>/. Labels come from the translation files, so
// the same script works in every UI language.

const lang = process.env.SHOTS_LANG ?? "en";
const out = path.resolve(import.meta.dirname, "../../docs/images", lang);
const messages = JSON.parse(
  readFileSync(path.resolve(import.meta.dirname, `../src/i18n/locales/${lang}.json`), "utf8"),
) as Record<string, unknown>;

function t(key: string, vars: Record<string, string> = {}): string {
  let v: unknown = messages;
  for (const part of key.split(".")) v = (v as Record<string, unknown>)[part];
  if (typeof v !== "string") throw new Error(`missing translation ${key}`);
  return v.replace(/\{\{(\w+)\}\}/g, (_, k: string) => vars[k] ?? "");
}

const PASSWORD = "correct horse battery staple";
const FLOW = "Engineering handbook";

async function shot(page: Page, name: string, fullPage = false) {
  await page.waitForLoadState("networkidle");
  // Notifications would cover the page.
  await page.addStyleTag({ content: "[data-sonner-toaster] { display: none !important; }" });
  await page.screenshot({ path: path.join(out, `${name}.png`), fullPage, animations: "disabled" });
}

async function nav(page: Page, key: string) {
  await page
    .getByRole("navigation", { name: t("nav.label") })
    .getByRole("link", { name: t(key) })
    .first()
    .click();
}

async function pick(page: Page, combobox: string, option: string, nth = 0) {
  await page.getByRole("combobox", { name: combobox }).nth(nth).click();
  await page.getByRole("option", { name: option }).click();
  await expect(page.getByRole("listbox")).toHaveCount(0);
}

async function addConnection(
  page: Page,
  kind: RegExp,
  name: string,
  fields: Record<string, string>,
  opts: { test?: boolean; shots?: [string, string] } = {},
) {
  await nav(page, "nav.connections");
  await page
    .getByRole("link", { name: t("connections.new") })
    .first()
    .click();
  await page.getByRole("radio", { name: kind }).click();
  if (opts.shots) {
    await page.evaluate("window.scrollTo(0, 0)");
    await shot(page, opts.shots[0]);
  }
  await page.getByLabel(t("common.name"), { exact: true }).fill(name);
  for (const [label, value] of Object.entries(fields)) {
    await page.getByLabel(label, { exact: true }).fill(value);
  }
  if (opts.test) {
    await page.getByRole("button", { name: t("connections.test") }).click();
    await expect(page.getByText(t("connections.testOk"))).toBeVisible();
  }
  if (opts.shots) await shot(page, opts.shots[1]);
  await page.getByRole("button", { name: t("connections.create") }).click();
  await expect(page.getByRole("cell", { name, exact: true })).toBeVisible();
}

test("guided tour", async ({ page }) => {
  mkdirSync(out, { recursive: true });
  await page.addInitScript((l) => globalThis.localStorage.setItem("tolarix-lang", l), lang);
  const env = (k: string) => process.env[k]!;

  // First start.
  await page.goto("/");
  await expect(page.getByRole("heading", { name: t("auth.signInTitle") })).toBeVisible();
  await page.getByLabel(t("auth.password"), { exact: true }).fill(env("SHOTS_PASSWORD"));
  await shot(page, "sign-in");
  await page.getByRole("button", { name: t("auth.signIn") }).click();
  await page.getByLabel(t("password.current"), { exact: true }).fill(env("SHOTS_PASSWORD"));
  await page.getByLabel(t("password.new"), { exact: true }).fill(PASSWORD);
  await page.getByLabel(t("password.confirm"), { exact: true }).fill(PASSWORD);
  await shot(page, "choose-password");
  await page.getByRole("button", { name: t("auth.choosePasswordSubmit") }).click();
  await expect(page.getByRole("heading", { level: 1, name: t("dashboard.title") })).toBeVisible();
  await shot(page, "dashboard-empty");

  // Connections: the Master vault (with screenshots), then the Mirrors.
  await addConnection(
    page,
    /Obsidian/,
    "Team handbook",
    { [t("connectorFields.obsidian.path")]: env("SHOTS_MASTER") },
    { test: true, shots: ["connection-type", "connection-form"] },
  );
  await addConnection(page, /Obsidian/, "Public vault", {
    [t("connectorFields.obsidian.path")]: env("SHOTS_MIRROR"),
  });
  await addConnection(page, /Local folder: Markdown/, "Docs site export", {
    [t("connectorFields.folder-markdown.path")]: env("SHOTS_SITE"),
  });
  await addConnection(page, /Local folder: GitHub wiki/, "GitHub wiki export", {
    [t("connectorFields.folder-github-wiki.path")]: env("SHOTS_GITHUB"),
  });
  // Saved without testing: these sample addresses do not exist.
  await addConnection(page, /Azure DevOps Code wiki/, "Engineering wiki (Azure DevOps)", {
    [t("connectorFields.azure-devops-code.url")]:
      "https://dev.azure.com/contoso/Platform/_wiki/wikis/Platform.wiki",
    [t("connectorFields.azure-devops-code.token")]: "sample-token",
  });
  await addConnection(page, /Confluence Cloud/, "Company space (Confluence)", {
    [t("connectorFields.confluence-cloud.url")]:
      "https://contoso.atlassian.net/wiki/spaces/ENG/overview",
    [t("connectorFields.confluence-cloud.email")]: "docs@contoso.example",
    [t("connectorFields.confluence-cloud.token")]: "sample-token",
  });
  await shot(page, "connections");

  // The flow wizard, one screenshot per step.
  await nav(page, "nav.flows");
  await page
    .getByRole("link", { name: t("flows.new") })
    .first()
    .click();
  const next = page.getByRole("button", { name: t("common.next") });
  await page.getByLabel(t("common.name"), { exact: true }).fill(FLOW);
  await pick(page, t("flowWizard.master"), "Team handbook");
  await shot(page, "flow-master");
  await next.click();
  await page.getByRole("checkbox", { name: "Drafts" }).click();
  await shot(page, "flow-scope");
  await next.click();
  const wiki = t("flowWizard.mirrorWiki");
  await pick(page, wiki, "Public vault");
  await page.getByLabel(t("connectorFields.obsidian.location.folder")).first().fill("Handbook");
  await page.getByRole("button", { name: t("flowWizard.addMirror") }).click();
  await pick(page, wiki, "Docs site export", 1);
  await page.getByLabel(t("connectorFields.folder-markdown.location.folder")).first().fill("docs");
  await page.getByRole("button", { name: t("flowWizard.addMirror") }).click();
  await pick(page, wiki, "GitHub wiki export", 2);
  await shot(page, "flow-mirrors", true);
  await next.click();
  await page.waitForTimeout(300);
  await shot(page, "flow-options", true);
  await next.click();
  await page.getByRole("radio", { name: t("schedule.kind.weekly") }).click();
  await expect(
    page
      .getByRole("region", { name: t("schedule.preview") })
      .getByRole("listitem")
      .first(),
  ).toBeVisible();
  await shot(page, "flow-schedule", true);
  await next.click();
  await page.waitForTimeout(300);
  await shot(page, "flow-review", true);
  await page.getByRole("button", { name: t("flowWizard.create") }).click();
  await expect(page.getByRole("heading", { level: 1, name: FLOW })).toBeVisible();

  // First run: live log and report.
  await page.getByRole("button", { name: t("flows.runNowFor", { name: FLOW }) }).click();
  await expect(page.getByText(t("logState.ended"))).toBeVisible({ timeout: 60_000 });
  await expect(page.getByText(t("runStatus.succeeded"), { exact: true })).toBeVisible();
  await shot(page, "run-log");
  await page.getByRole("tab", { name: t("runs.report") }).click();
  await page.locator("summary").first().click(); // the first Mirror's warnings
  await shot(page, "run-report");

  // A page changes in the Master; the next run updates only that page.
  writeFileSync(
    path.join(env("SHOTS_MASTER"), "Runbooks", "Deploy.md"),
    "# Deploy\n\n> [!warning] Freeze\n> No deploys on Fridays or holidays.\n\nSee also [[Ideas]] for what comes next.\n",
  );
  await page.getByRole("link", { name: FLOW }).first().click();
  await expect(page.getByRole("heading", { level: 1, name: FLOW })).toBeVisible();
  await page.getByRole("button", { name: t("flows.runNowFor", { name: FLOW }) }).click();
  await expect(page.getByText(t("logState.ended"))).toBeVisible({ timeout: 60_000 });
  await page.getByRole("link", { name: FLOW }).first().click();
  await expect(page.getByRole("heading", { level: 1, name: FLOW })).toBeVisible();
  // Not while a run is shown as running: that banner names the host.
  await expect(page.getByText(t("runStatus.running"), { exact: true })).toHaveCount(0);
  await shot(page, "flow-detail");

  // A second, smaller flow: only the onboarding pages, run by hand, tried
  // with a dry-run first.
  const second = "Onboarding for partners";
  await nav(page, "nav.flows");
  await page
    .getByRole("link", { name: t("flows.new") })
    .first()
    .click();
  await page.getByLabel(t("common.name"), { exact: true }).fill(second);
  await pick(page, t("flowWizard.master"), "Team handbook");
  await next.click();
  await page.getByRole("radio", { name: t("flowWizard.scope.selected") }).click();
  await page.getByRole("checkbox", { name: "Onboarding" }).click();
  await next.click();
  await pick(page, wiki, "Public vault");
  await page.getByLabel(t("connectorFields.obsidian.location.folder")).first().fill("Partners");
  for (let i = 0; i < 3; i++) await next.click();
  await page.getByRole("button", { name: t("flowWizard.create") }).click();
  await expect(page.getByRole("heading", { level: 1, name: second })).toBeVisible();
  await page.getByRole("button", { name: t("flows.dryRunFor", { name: second }) }).click();
  await expect(page.getByText(t("runStatus.succeeded"), { exact: true })).toBeVisible({
    timeout: 60_000,
  });

  await nav(page, "nav.runs");
  await expect(page.getByRole("cell", { name: FLOW, exact: true }).first()).toBeVisible();
  await shot(page, "runs");

  await nav(page, "nav.dashboard");
  await expect(page.getByRole("heading", { level: 1, name: t("dashboard.title") })).toBeVisible();
  await expect(page.getByRole("link", { name: FLOW }).first()).toBeVisible();
  await shot(page, "dashboard");
  await page.getByRole("button", { name: t("theme.toggle") }).click();
  await page.getByRole("menuitemradio", { name: t("theme.dark") }).click();
  await shot(page, "dashboard-dark");
});
