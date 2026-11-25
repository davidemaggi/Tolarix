import { Pencil, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import { useConnections, useDeleteFlow, useFlow, useRuns } from "@/api/queries";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { PageHeader } from "@/components/page-header";
import { BackLink, QueryError } from "@/components/query-state";
import { RunButtons } from "@/components/run-buttons";
import { RunStatusBadge } from "@/components/run-status";
import { RunTable } from "@/components/run-table";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { errorMessage } from "@/lib/errors";

export function FlowDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const navigate = useNavigate();
  const flow = useFlow(id);
  const connections = useConnections();
  const runs = useRuns({ flowId: id }, 10);
  const del = useDeleteFlow();
  const [confirm, setConfirm] = useState(false);
  const nameOf = (cid: string) => connections.data?.find((c) => c.id === cid)?.name ?? "…";

  const back = <BackLink to="/flows">{t("flows.title")}</BackLink>;
  if (flow.isPending) {
    return (
      <>
        <PageHeader before={back} title={<Skeleton className="h-8 w-64" />} />
        <Skeleton className="h-48" />
      </>
    );
  }
  if (flow.isError) {
    return (
      <>
        <PageHeader before={back} title={t("flows.title")} />
        <QueryError error={flow.error} onRetry={() => void flow.refetch()} />
      </>
    );
  }
  const f = flow.data;
  const running = !!f.running;
  const recent = runs.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <>
      <PageHeader
        before={back}
        title={f.name}
        actions={
          <>
            <RunButtons flow={f} size="default" />
            {running ? (
              <Button variant="outline" disabled title={t("flows.busyHint")}>
                <Pencil aria-hidden="true" />
                {t("common.edit")}
              </Button>
            ) : (
              <Button variant="outline" asChild>
                <Link to={`/flows/${f.id}/edit`}>
                  <Pencil aria-hidden="true" />
                  {t("common.edit")}
                </Link>
              </Button>
            )}
            <Button
              variant="outline"
              onClick={() => setConfirm(true)}
              disabled={running}
              title={running ? t("flows.busyHint") : undefined}
            >
              <Trash2 aria-hidden="true" />
              {t("common.delete")}
            </Button>
          </>
        }
      />

      {f.running && (
        <p className="mb-6 flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-4 py-3 text-sm">
          <RunStatusBadge status="running" />
          <span>{t("flows.runningBy", { holder: f.running.holder })}</span>
          <Link to={`/runs/${f.running.runId}`} className="underline underline-offset-4">
            {t("dashboard.followLive")}
          </Link>
        </p>
      )}

      <dl className="mb-10 grid gap-x-8 gap-y-4 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-muted-foreground">{t("flowWizard.master")}</dt>
          <dd className="font-medium">{nameOf(f.sourceConnectionId)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{t("flowWizard.step.mirrors")}</dt>
          <dd className="font-medium">{f.targets.map((m) => nameOf(m.connectionId)).join(", ")}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{t("flowWizard.step.scope")}</dt>
          <dd>
            {f.scope.mode === "all"
              ? t("flowWizard.scope.all")
              : t("flows.scopeSelected", { count: f.scope.include?.length ?? 0 })}
            {!!f.scope.exclude?.length &&
              ` · ${t("flows.scopeExcluded", { count: f.scope.exclude.length })}`}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{t("flowWizard.deletePolicy")}</dt>
          <dd>{t(`flowWizard.policy.${f.options.deletePolicy ?? "archive"}`)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{t("flowWizard.step.schedule")}</dt>
          <dd>{t("flowWizard.manualOnly")}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{t("flows.state")}</dt>
          <dd>{f.enabled ? t("flows.enabled") : t("flows.disabled")}</dd>
        </div>
      </dl>

      <section aria-labelledby="recent-runs" className="space-y-4">
        <div className="flex items-center justify-between gap-4">
          <h2 id="recent-runs" className="text-lg font-medium">
            {t("flows.recentRuns")}
          </h2>
          <Link
            to={`/runs?flow=${encodeURIComponent(f.id)}`}
            className="text-sm underline-offset-4 hover:underline"
          >
            {t("flows.allRuns")}
          </Link>
        </div>
        {runs.isPending ? (
          <Skeleton className="h-24" />
        ) : runs.isError ? (
          <QueryError error={runs.error} onRetry={() => void runs.refetch()} />
        ) : recent.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("dashboard.neverRun")}</p>
        ) : (
          <RunTable runs={recent} showFlow={false} />
        )}
      </section>

      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t("flows.deleteTitle", { name: f.name })}
        description={t("flows.deleteBody")}
        confirmLabel={t("common.delete")}
        destructive
        disabled={del.isPending}
        onConfirm={() =>
          del.mutate(f.id, {
            onSuccess: () => {
              toast.success(t("flows.deleted", { name: f.name }));
              void navigate("/flows");
            },
            onError: (err) => {
              setConfirm(false);
              toast.error(errorMessage(err, t));
            },
          })
        }
      />
    </>
  );
}
