import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

// WCAG 2.2 level A and AA rules (axe also checks best practices it tags
// separately; those are not enforced here).
const TAGS = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

/** Fails with a readable list when axe finds WCAG A/AA violations on the page. */
export async function expectAccessible(page: Page, where: string) {
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
