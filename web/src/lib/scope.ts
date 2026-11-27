import type { Scope } from "@/api/client";

/**
 * Scope rules, matching the engine (core/internal/engine/scope.go):
 * a page is mirrored when the mode is "all", or it or an ancestor is
 * included; an excluded page or ancestor always wins.
 *
 * `path` is the page's ancestors from the top, followed by the page itself.
 */
export type NodeState = {
  checked: boolean;
  /** An ancestor is excluded, so this page cannot be selected. */
  locked: boolean;
  /** Checked because of an ancestor, not by itself. */
  inherited: boolean;
};

export function nodeState(scope: Scope, path: string[]): NodeState {
  const id = path[path.length - 1]!;
  const ancestors = path.slice(0, -1);
  const exclude = new Set(scope.exclude ?? []);
  const include = new Set(scope.include ?? []);
  if (ancestors.some((a) => exclude.has(a))) {
    return { checked: false, locked: true, inherited: true };
  }
  if (exclude.has(id)) return { checked: false, locked: false, inherited: false };
  if (scope.mode === "all") return { checked: true, locked: false, inherited: true };
  if (include.has(id)) return { checked: true, locked: false, inherited: false };
  const byAncestor = ancestors.some((a) => include.has(a));
  return { checked: byAncestor, locked: false, inherited: byAncestor };
}

/** Flip a page between mirrored and not mirrored. */
export function toggleNode(scope: Scope, path: string[]): Scope {
  const id = path[path.length - 1]!;
  const state = nodeState(scope, path);
  if (state.locked) return scope;
  let include = (scope.include ?? []).filter((x) => x !== id);
  let exclude = (scope.exclude ?? []).filter((x) => x !== id);
  const next = (): Scope => ({ mode: scope.mode, include, exclude });
  if (state.checked) {
    if (nodeState(next(), path).checked) exclude = [...exclude, id];
  } else {
    if (!nodeState(next(), path).checked) {
      include = [...include, id];
      // Pages below are covered by this one now.
      exclude = exclude.filter((x) => x !== id);
    }
  }
  return normalize(next());
}

/** Drop empty lists and duplicates; mode "all" keeps no include list. */
export function normalize(scope: Scope): Scope {
  const out: Scope = { mode: scope.mode };
  const inc = [...new Set(scope.include ?? [])];
  const exc = [...new Set(scope.exclude ?? [])];
  if (scope.mode === "selected" && inc.length) out.include = inc;
  if (exc.length) out.exclude = exc;
  return out;
}
