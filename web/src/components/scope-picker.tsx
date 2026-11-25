import { ChevronRight, FileText, Folder, Loader2, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { BrowseNode, Scope } from "@/api/client";
import { useBrowse } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { errorMessage } from "@/lib/errors";
import { nodeState, toggleNode } from "@/lib/scope";
import { cn } from "@/lib/utils";

/**
 * A lazily loaded page tree of the Master. Ticking a page mirrors it with
 * everything below it; unticking a page inside a ticked one leaves it out.
 * `titles` maps page ids to names for the summary (see usePageTitles).
 */
export function ScopePicker({
  connectionId,
  scope,
  onChange,
  titles,
}: {
  connectionId: string;
  scope: Scope;
  onChange: (s: Scope) => void;
  titles: ReadonlyMap<string, string>;
}) {
  const { t } = useTranslation();
  return (
    <div className="space-y-3">
      <div className="max-h-[28rem] overflow-auto rounded-lg border p-2">
        <Children
          connectionId={connectionId}
          parent=""
          path={[]}
          scope={scope}
          onChange={onChange}
          titles={titles}
          level={1}
          label={t("flowWizard.scopeTree")}
        />
      </div>
      <ScopeSummary scope={scope} onChange={onChange} titles={titles} />
    </div>
  );
}

type ChildrenProps = {
  connectionId: string;
  parent: string;
  path: string[];
  scope: Scope;
  onChange: (s: Scope) => void;
  titles: ReadonlyMap<string, string>;
  level: number;
  label?: string;
};

function Children(props: ChildrenProps) {
  const { t } = useTranslation();
  const q = useBrowse(props.connectionId, props.parent);
  if (q.isPending) {
    return (
      <p className="flex items-center gap-2 px-2 py-1 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        {t("common.loading")}
      </p>
    );
  }
  if (q.isError) {
    return (
      <div role="alert" className="space-y-2 px-2 py-1 text-sm">
        <p className="text-destructive">{errorMessage(q.error, t)}</p>
        <Button type="button" variant="outline" size="sm" onClick={() => void q.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }
  if (!q.data.length) {
    return (
      <p className="px-2 py-1 text-sm text-muted-foreground">
        {props.parent ? t("flowWizard.noChildren") : t("flowWizard.emptyMaster")}
      </p>
    );
  }
  return (
    <ul aria-label={props.label}>
      {q.data.map((n) => (
        <TreeNode key={n.id} node={n} {...props} label={undefined} />
      ))}
    </ul>
  );
}

function TreeNode({ node, ...props }: ChildrenProps & { node: BrowseNode }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const path = [...props.path, node.id];
  const state = nodeState(props.scope, path);
  const Icon = node.hasChildren ? Folder : FileText;
  const checkboxId = `scope-${node.id}`;

  return (
    <li>
      <div
        className={cn(
          "flex items-center gap-1 rounded-md py-0.5 pr-2 hover:bg-accent/60",
          state.locked && "opacity-60",
        )}
        style={{ paddingLeft: `${(props.level - 1) * 1.25}rem` }}
      >
        {node.hasChildren ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-7"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            aria-label={t("flowWizard.expand", { name: node.title })}
          >
            <ChevronRight
              className={cn("transition-transform", open && "rotate-90")}
              aria-hidden="true"
            />
          </Button>
        ) : (
          <span className="size-7 shrink-0" />
        )}
        <Checkbox
          id={checkboxId}
          checked={state.checked}
          disabled={state.locked}
          onCheckedChange={() => props.onChange(toggleNode(props.scope, path))}
        />
        <label
          htmlFor={checkboxId}
          className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 py-1 pl-1 text-sm"
        >
          <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="truncate">{node.title}</span>
          {state.checked && !state.inherited && props.scope.mode === "selected" && (
            <span className="sr-only">({t("flowWizard.selected")})</span>
          )}
        </label>
      </div>
      {open && (
        <Children
          {...props}
          parent={node.id}
          path={path}
          level={props.level + 1}
          label={node.title}
        />
      )}
    </li>
  );
}

/** Lists the explicit choices, so the user can undo them without the tree. */
function ScopeSummary({
  scope,
  onChange,
  titles,
}: {
  scope: Scope;
  onChange: (s: Scope) => void;
  titles: ReadonlyMap<string, string>;
}) {
  const { t } = useTranslation();
  const include = scope.mode === "selected" ? (scope.include ?? []) : [];
  const exclude = scope.exclude ?? [];
  const remove = (list: "include" | "exclude", id: string) =>
    onChange({ ...scope, [list]: (scope[list] ?? []).filter((x) => x !== id) });

  return (
    <div className="space-y-2 text-sm">
      {scope.mode === "selected" && (
        <ChipList
          label={t("flowWizard.included")}
          empty={t("flowWizard.nothingSelected")}
          ids={include}
          titles={titles}
          onRemove={(id) => remove("include", id)}
        />
      )}
      {exclude.length > 0 && (
        <ChipList
          label={t("flowWizard.excluded")}
          ids={exclude}
          titles={titles}
          onRemove={(id) => remove("exclude", id)}
        />
      )}
    </div>
  );
}

function ChipList({
  label,
  empty,
  ids,
  titles,
  onRemove,
}: {
  label: string;
  empty?: string;
  ids: string[];
  titles: ReadonlyMap<string, string>;
  onRemove: (id: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="font-medium">{label}:</span>
      {ids.length === 0 && <span className="text-muted-foreground">{empty}</span>}
      {ids.map((id) => {
        const title = titles.get(id) ?? id;
        return (
          <span
            key={id}
            className="inline-flex items-center gap-1 rounded-full border bg-muted/50 py-0.5 pr-1 pl-2.5"
          >
            <span className="max-w-[16rem] truncate" title={id}>
              {title}
            </span>
            <button
              type="button"
              onClick={() => onRemove(id)}
              className="rounded-full p-0.5 hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
              aria-label={t("flowWizard.removeChoice", { name: title })}
            >
              <X className="size-3.5" aria-hidden="true" />
            </button>
          </span>
        );
      })}
    </div>
  );
}
