import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { useConnections, useFlows } from "@/api/queries";
import { PageHeader } from "@/components/page-header";
import { QueryError } from "@/components/query-state";
import { RunButtons } from "@/components/run-buttons";
import { RunStatusBadge } from "@/components/run-status";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useFormatters } from "@/lib/format";

export function FlowsPage() {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const flows = useFlows();
  const connections = useConnections();
  const nameOf = (id: string) => connections.data?.find((c) => c.id === id)?.name ?? "…";

  return (
    <>
      <PageHeader
        title={t("flows.title")}
        intro={t("flows.intro")}
        actions={
          <Button asChild>
            <Link to="/flows/new">
              <Plus aria-hidden="true" />
              {t("flows.new")}
            </Link>
          </Button>
        }
      />
      {flows.isPending ? (
        <Skeleton className="h-32" />
      ) : flows.isError ? (
        <QueryError error={flows.error} onRetry={() => void flows.refetch()} />
      ) : flows.data.length === 0 ? (
        <p className="rounded-xl border border-dashed px-6 py-10 text-center text-muted-foreground">
          {t("flows.empty")}
        </p>
      ) : (
        <div className="rounded-xl border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("common.name")}</TableHead>
                <TableHead>{t("flowWizard.master")}</TableHead>
                <TableHead>{t("flowWizard.step.mirrors")}</TableHead>
                <TableHead>{t("flows.lastRun")}</TableHead>
                <TableHead className="w-0">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {flows.data.map((f) => (
                <TableRow key={f.id}>
                  <TableCell className="font-medium">
                    <Link to={`/flows/${f.id}`} className="hover:underline">
                      {f.name}
                    </Link>
                    {!f.enabled && (
                      <span className="ml-2 text-xs font-normal text-muted-foreground">
                        ({t("flows.disabled")})
                      </span>
                    )}
                  </TableCell>
                  <TableCell>{nameOf(f.sourceConnectionId)}</TableCell>
                  <TableCell>{f.targets.map((m) => nameOf(m.connectionId)).join(", ")}</TableCell>
                  <TableCell>
                    {f.running ? (
                      <Link to={`/runs/${f.running.runId}`}>
                        <RunStatusBadge status="running" />
                      </Link>
                    ) : f.lastRun ? (
                      <Link to={`/runs/${f.lastRun.id}`} className="flex flex-col gap-1">
                        <RunStatusBadge status={f.lastRun.status} dryRun={f.lastRun.dryRun} />
                        <time
                          dateTime={f.lastRun.startedAt}
                          className="text-xs text-muted-foreground"
                        >
                          {fmt.dateTime(f.lastRun.startedAt)}
                        </time>
                      </Link>
                    ) : (
                      <span className="text-muted-foreground">{t("dashboard.neverRun")}</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <RunButtons flow={f} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  );
}
