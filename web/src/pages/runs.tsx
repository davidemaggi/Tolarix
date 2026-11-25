import { useTranslation } from "react-i18next";
import { useSearchParams } from "react-router";

import type { RunStatus } from "@/api/client";
import { useFlows, useRuns, type RunFilters } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { QueryError } from "@/components/query-state";
import { RunTable } from "@/components/run-table";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";

const STATUSES: RunStatus[] = ["running", "succeeded", "partial", "failed", "cancelled"];
const ANY = "any";

/** Run history; the filters live in the URL so links and Back work. */
export function RunsPage() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const flows = useFlows();

  const flowId = params.get("flow") ?? undefined;
  const status = (params.get("status") as RunStatus | null) ?? undefined;
  const kind = params.get("kind"); // "dry" | "real"
  const filters: RunFilters = {
    flowId,
    status: status && STATUSES.includes(status) ? status : undefined,
    dryRun: kind === "dry" ? true : kind === "real" ? false : undefined,
  };
  const runs = useRuns(filters);

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value === ANY) next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };
  const items = runs.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <>
      <PageHeader title={t("runs.title")} intro={t("runs.intro")} />
      <div className="mb-6 flex flex-wrap gap-4">
        <FilterSelect
          id="filter-flow"
          label={t("runs.flow")}
          value={flowId ?? ANY}
          onChange={(v) => setParam("flow", v)}
          options={[
            { value: ANY, label: t("runs.allFlows") },
            ...(flows.data ?? []).map((f) => ({ value: f.id, label: f.name })),
          ]}
        />
        <FilterSelect
          id="filter-status"
          label={t("runs.status")}
          value={filters.status ?? ANY}
          onChange={(v) => setParam("status", v)}
          options={[
            { value: ANY, label: t("runs.anyStatus") },
            ...STATUSES.map((s) => ({ value: s, label: t(`runStatus.${s}`) })),
          ]}
        />
        <FilterSelect
          id="filter-kind"
          label={t("runs.kind")}
          value={kind === "dry" || kind === "real" ? kind : ANY}
          onChange={(v) => setParam("kind", v)}
          options={[
            { value: ANY, label: t("runs.allKinds") },
            { value: "real", label: t("runs.realOnly") },
            { value: "dry", label: t("runs.dryOnly") },
          ]}
        />
      </div>
      {runs.isPending ? (
        <Skeleton className="h-48" />
      ) : runs.isError ? (
        <QueryError error={runs.error} onRetry={() => void runs.refetch()} />
      ) : items.length === 0 ? (
        <p className="rounded-xl border border-dashed px-6 py-10 text-center text-muted-foreground">
          {flowId || filters.status || filters.dryRun !== undefined
            ? t("runs.noMatch")
            : t("runs.empty")}
        </p>
      ) : (
        <div className="space-y-4">
          <RunTable runs={items} />
          {runs.hasNextPage && (
            <Button
              variant="outline"
              onClick={() => void runs.fetchNextPage()}
              disabled={runs.isFetchingNextPage}
            >
              {t("runs.loadMore")}
            </Button>
          )}
        </div>
      )}
    </>
  );
}

function FilterSelect({
  id,
  label,
  value,
  onChange,
  options,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: { value: string; label: string }[];
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger id={id} className="w-52">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o.value} value={o.value}>
              {o.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  );
}
