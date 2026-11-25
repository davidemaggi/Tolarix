import { ArrowRight, Cable, Plus, Workflow } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { Connection, Flow } from "@/api/client";
import { useConnections, useFlows } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { QueryError } from "@/components/query-state";
import { RunButtons } from "@/components/run-buttons";
import { CountsSummary, RunStatusBadge } from "@/components/run-status";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useFormatters } from "@/lib/format";

export function DashboardPage() {
  const { t } = useTranslation();
  const flows = useFlows();
  const connections = useConnections();

  const byId = new Map((connections.data ?? []).map((c) => [c.id, c]));

  return (
    <>
      <PageHeader
        title={t("dashboard.title")}
        intro={t("dashboard.intro")}
        actions={
          flows.data?.length ? (
            <Button asChild>
              <Link to="/flows/new">
                <Plus aria-hidden="true" />
                {t("flows.new")}
              </Link>
            </Button>
          ) : undefined
        }
      />
      {flows.isPending ? (
        <div className="grid gap-4 md:grid-cols-2">
          <Skeleton className="h-40" />
          <Skeleton className="h-40" />
        </div>
      ) : flows.isError ? (
        <QueryError error={flows.error} onRetry={() => void flows.refetch()} />
      ) : flows.data.length === 0 ? (
        <GettingStarted hasConnections={(connections.data?.length ?? 0) > 0} />
      ) : (
        <ul className="grid gap-4 md:grid-cols-2" aria-label={t("flows.title")}>
          {flows.data.map((f) => (
            <li key={f.id}>
              <FlowCard flow={f} connections={byId} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

function FlowCard({ flow, connections }: { flow: Flow; connections: Map<string, Connection> }) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const master = connections.get(flow.sourceConnectionId)?.name ?? "…";
  const mirrors = flow.targets.map((m) => connections.get(m.connectionId)?.name ?? "…");
  const last = flow.lastRun;

  return (
    <article className="flex h-full flex-col gap-4 rounded-xl border bg-card p-5 text-card-foreground">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h2 className="truncate text-base font-semibold">
            <Link to={`/flows/${flow.id}`} className="hover:underline">
              {flow.name}
            </Link>
          </h2>
          <p className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground">
            <span className="font-medium text-foreground">{master}</span>
            <ArrowRight className="size-3.5" aria-label={t("flows.mirroredTo")} />
            <span>{mirrors.join(", ")}</span>
          </p>
        </div>
        {!flow.enabled && (
          <span className="shrink-0 rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
            {t("flows.disabled")}
          </span>
        )}
      </div>

      <div className="flex-1 space-y-1 text-sm">
        {flow.running ? (
          <p className="flex flex-wrap items-center gap-2">
            <RunStatusBadge status="running" />
            <Link to={`/runs/${flow.running.runId}`} className="underline underline-offset-4">
              {t("dashboard.followLive")}
            </Link>
          </p>
        ) : last ? (
          <>
            <p className="flex flex-wrap items-center gap-2">
              <RunStatusBadge status={last.status} dryRun={last.dryRun} />
              <Link
                to={`/runs/${last.id}`}
                className="text-muted-foreground underline-offset-4 hover:underline"
              >
                <time dateTime={last.startedAt}>{fmt.dateTime(last.startedAt)}</time>
              </Link>
            </p>
            <CountsSummary counts={last.counts} className="block text-muted-foreground" />
          </>
        ) : (
          <p className="text-muted-foreground">{t("dashboard.neverRun")}</p>
        )}
      </div>

      <RunButtons flow={flow} />
    </article>
  );
}

function GettingStarted({ hasConnections }: { hasConnections: boolean }) {
  const { t } = useTranslation();
  return (
    <section
      aria-labelledby="getting-started"
      className="flex flex-col items-center gap-4 rounded-xl border border-dashed px-6 py-14 text-center"
    >
      <Workflow className="size-10 text-muted-foreground" aria-hidden="true" />
      <h2 id="getting-started" className="text-lg font-medium">
        {t("dashboard.emptyTitle")}
      </h2>
      <p className="max-w-md text-sm text-muted-foreground">{t("dashboard.emptyBody")}</p>
      <ol className="flex flex-col gap-2 text-left text-sm sm:flex-row sm:gap-6">
        <li className={hasConnections ? "text-muted-foreground line-through" : ""}>
          1. {t("dashboard.stepConnections")}
        </li>
        <li>2. {t("dashboard.stepFlow")}</li>
        <li>3. {t("dashboard.stepRun")}</li>
      </ol>
      {hasConnections ? (
        <Button asChild>
          <Link to="/flows/new">
            <Plus aria-hidden="true" />
            {t("flows.new")}
          </Link>
        </Button>
      ) : (
        <Button asChild>
          <Link to="/connections/new">
            <Cable aria-hidden="true" />
            {t("connections.new")}
          </Link>
        </Button>
      )}
    </section>
  );
}
