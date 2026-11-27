/// <reference lib="dom" /> // the waitForFunction callback runs in the page
import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

// WCAG 2.2 level A and AA rules (axe also checks best practices it tags
// separately; those are not enforced here).
const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

/** Fails with a readable list when axe finds WCAG A/AA violations on the page. */
export async function expectAccessible(page: Page, where: string) {
  // Check the settled page: mid-transition colors (e.g. a toast fading in)
  // are not what the user reads.
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .every((a) => a.playState !== "running" || a.effect?.getTiming().iterations === Infinity),
  );
  const { violations } = await new AxeBuilder({ page }).withTags(TAGS).analyze();
  const found = violations.map(
    (v) =>
      `${v.id} (${v.impact}): ${v.help}\n` +
      v.nodes
        .slice(0, 5)
        .map((n) => `    ${n.target.join(" ")}: ${n.failureSummary?.split("\n")[1]?.trim() ?? ""}`)
        .join("\n"),
  );
  expect(found, `accessibility violations on ${where}`).toEqual([]);
}
