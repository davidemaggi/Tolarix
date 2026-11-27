import type { Scope } from "@/api/client";

import { nodeState, normalize, toggleNode } from "./scope";

// Tree used below: a > b > c, and d at the top.
const a = ["a"];
const b = ["a", "b"];
const c = ["a", "b", "c"];
const d = ["d"];

describe("nodeState", () => {
  it("checks everything in mode all, inherited", () => {
    expect(nodeState({ mode: "all" }, c)).toEqual({
      checked: true,
      locked: false,
      inherited: true,
    });
  });

  it("checks an included page and its descendants", () => {
    const s: Scope = { mode: "selected", include: ["a"] };
    expect(nodeState(s, a)).toEqual({ checked: true, locked: false, inherited: false });
    expect(nodeState(s, c)).toEqual({ checked: true, locked: false, inherited: true });
    expect(nodeState(s, d).checked).toBe(false);
  });

  it("lets an exclusion win over an inclusion and lock the subtree", () => {
    const s: Scope = { mode: "selected", include: ["a", "c"], exclude: ["b"] };
    expect(nodeState(s, b)).toEqual({ checked: false, locked: false, inherited: false });
    expect(nodeState(s, c)).toEqual({ checked: false, locked: true, inherited: true });
  });
});

describe("toggleNode", () => {
  it("includes a page in mode selected", () => {
    expect(toggleNode({ mode: "selected" }, b)).toEqual({ mode: "selected", include: ["b"] });
  });

  it("excludes a page covered by an ancestor", () => {
    expect(toggleNode({ mode: "selected", include: ["a"] }, c)).toEqual({
      mode: "selected",
      include: ["a"],
      exclude: ["c"],
    });
  });

  it("excludes a page in mode all and re-includes it", () => {
    const off = toggleNode({ mode: "all" }, b);
    expect(off).toEqual({ mode: "all", exclude: ["b"] });
    expect(toggleNode(off, b)).toEqual({ mode: "all" });
  });

  it("unticks an included page without leaving it excluded", () => {
    expect(toggleNode({ mode: "selected", include: ["b"] }, b)).toEqual({ mode: "selected" });
  });

  it("does nothing on a page locked by an excluded ancestor", () => {
    const s: Scope = { mode: "all", exclude: ["a"] };
    expect(toggleNode(s, c)).toBe(s);
  });
});

describe("normalize", () => {
  it("drops duplicates, empty lists and includes in mode all", () => {
    expect(normalize({ mode: "all", include: ["a"], exclude: ["b", "b"] })).toEqual({
      mode: "all",
      exclude: ["b"],
    });
    expect(normalize({ mode: "selected", include: [], exclude: [] })).toEqual({
      mode: "selected",
    });
  });
});
