import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { Run } from "@/api/client";
import { CountsSummary, RunStatusBadge } from "@/components/run-status";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useDuration, useFormatters } from "@/lib/format";

export function RunTable({ runs, showFlow = true }: { runs: Run[]; showFlow?: boolean }) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const duration = useDuration();
  return (
    <div className="rounded-xl border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t("runs.started")}</TableHead>
            {showFlow && <TableHead>{t("runs.flow")}</TableHead>}
            <TableHead>{t("runs.status")}</TableHead>
            <TableHead>{t("runs.changes")}</TableHead>
            <TableHead>{t("runs.trigger")}</TableHead>
            <TableHead>{t("runs.duration")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {runs.map((r) => (
            <TableRow key={r.id}>
              <TableCell>
                <Link to={`/runs/${r.id}`} className="font-medium hover:underline">
                  <time dateTime={r.startedAt}>{fmt.dateTime(r.startedAt)}</time>
                </Link>
              </TableCell>
              {showFlow && (
                <TableCell>
                  <Link to={`/flows/${r.flowId}`} className="hover:underline">
                    {r.flowName}
                  </Link>
                </TableCell>
              )}
              <TableCell>
                <RunStatusBadge status={r.status} dryRun={r.dryRun} />
              </TableCell>
              <TableCell className="text-sm">
                <CountsSummary counts={r.counts} />
              </TableCell>
              <TableCell className="text-muted-foreground">
                {t(`runTrigger.${r.trigger}`)}
              </TableCell>
              <TableCell className="text-muted-foreground tabular-nums">
                {duration(r.startedAt, r.finishedAt)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
