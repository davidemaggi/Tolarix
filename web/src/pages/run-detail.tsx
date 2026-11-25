import { CircleStop } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useParams } from "react-router";
import { toast } from "sonner";

import type { Counts, RunDetail, RunEvent, Schemas } from "@/api/client";
import { useCancelRun, useRun } from "@/api/queries";
import { useRunStream, type StreamState } from "@/api/run-stream";
import { PageHeader } from "@/components/page-header";
import { BackLink, QueryError } from "@/components/query-state";
import { RunStatusBadge } from "@/components/run-status";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { errorMessage } from "@/lib/errors";
import { useDuration, useFormatters } from "@/lib/format";
import { cn } from "@/lib/utils";

export function RunDetailPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const run = useRun(id);
  const back = <BackLink to="/runs">{t("runs.title")}</BackLink>;

  if (run.isPending) {
    return (
      <>
        <PageHeader before={back} title={<Skeleton className="h-8 w-72" />} />
        <Skeleton className="h-64" />
      </>
    );
  }
  if (run.isError) {
    return (
      <>
        <PageHeader before={back} title={t("runs.run")} />
        <QueryError error={run.error} onRetry={() => void run.refetch()} />
      </>
    );
  }
  return <RunView key={run.data.id} run={run.data} back={back} />;
}

function RunView({ run, back }: { run: RunDetail; back: React.ReactNode }) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const duration = useDuration();
  const cancel = useCancelRun();
  const stream = useRunStream(run.id);
  const running = run.status === "running" && stream.state !== "ended";
  const [tab, setTab] = useState(run.status === "running" ? "log" : "report");

  return (
    <>
      <PageHeader
        before={back}
        title={
          <>
            <Link to={`/flows/${run.flowId}`} className="hover:underline">
              {run.flowName}
            </Link>
            <span className="text-muted-foreground">
              {" "}
              · <time dateTime={run.startedAt}>{fmt.dateTime(run.startedAt)}</time>
            </span>
          </>
        }
        actions={
          running && (
            <Button
              variant="outline"
              disabled={cancel.isPending || cancel.isSuccess}
              onClick={() =>
                cancel.mutate(run.id, {
                  onSuccess: () => toast.info(t("runs.cancelRequested")),
                  onError: (err) => toast.error(errorMessage(err, t)),
                })
              }
            >
              <CircleStop aria-hidden="true" />
              {t("runs.cancel")}
            </Button>
          )
        }
      />

      <div className="mb-8 space-y-4">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
          <RunStatusBadge status={running ? "running" : run.status} dryRun={run.dryRun} />
          <span className="text-muted-foreground">{t(`runTrigger.${run.trigger}`)}</span>
          {run.finishedAt && (
            <span className="text-muted-foreground">
              {t("runs.tookTime", { duration: duration(run.startedAt, run.finishedAt) })}
            </span>
          )}
        </div>
        {run.dryRun && <p className="text-sm text-muted-foreground">{t("runs.dryRunNote")}</p>}
        {run.error && (
          <p
            role="alert"
            className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm"
          >
            {run.error}
          </p>
        )}
        {!running && <CountsGrid counts={run.counts} />}
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="report">{t("runs.report")}</TabsTrigger>
          <TabsTrigger value="log">{t("runs.log")}</TabsTrigger>
        </TabsList>
        <TabsContent value="report" className="pt-4">
          {running ? (
            <p className="text-sm text-muted-foreground">{t("runs.reportPending")}</p>
          ) : (
            <Report run={run} />
          )}
        </TabsContent>
        <TabsContent value="log" className="pt-4" forceMount hidden={tab !== "log"}>
          <LiveLog events={stream.events} state={stream.state} truncated={stream.truncated} />
        </TabsContent>
      </Tabs>
    </>
  );
}

const shownCounts = [
  "created",
  "updated",
  "moved",
  "archived",
  "deleted",
  "unchanged",
  "failed",
  "attachmentsUploaded",
] as const satisfies readonly (keyof Counts)[];

function CountsGrid({ counts }: { counts: Counts }) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  return (
    <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-8">
      {shownCounts.map((k) => (
        <div key={k} className="rounded-lg border px-3 py-2">
          <dt className="text-xs text-muted-foreground">{t(`countLabels.${k}`)}</dt>
          <dd
            className={cn(
              "text-lg font-semibold tabular-nums",
              k === "failed" && counts[k] > 0 && "text-destructive",
            )}
          >
            {fmt.number(counts[k])}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function Report({ run }: { run: RunDetail }) {
  const { t } = useTranslation();
  const [showUnchanged, setShowUnchanged] = useState(false);
  const r = run.report;
  return (
    <div className="space-y-8">
      <p className="text-sm text-muted-foreground">
        {t("runs.pagesInScope", { count: r.pages ?? 0 })}
      </p>
      <Warnings title={t("runs.masterWarnings")} warnings={r.warnings} />
      {r.targets.length > 0 && (
        <div className="flex items-center gap-2">
          <Switch id="show-unchanged" checked={showUnchanged} onCheckedChange={setShowUnchanged} />
          <Label htmlFor="show-unchanged">{t("runs.showUnchanged")}</Label>
        </div>
      )}
      {r.targets.map((tr) => (
        <TargetSection key={tr.targetId} target={tr} showUnchanged={showUnchanged} />
      ))}
    </div>
  );
}

function TargetSection({
  target,
  showUnchanged,
}: {
  target: Schemas["TargetReport"];
  showUnchanged: boolean;
}) {
  const { t } = useTranslation();
  const ops = showUnchanged ? target.ops : target.ops.filter((o) => o.kind !== "unchanged");
  const headingId = `target-${target.targetId}`;
  return (
    <section aria-labelledby={headingId} className="space-y-3">
      <h3 id={headingId} className="font-medium">
        {t("runs.mirrorTitle", { name: target.name })}
      </h3>
      {target.error && (
        <p
          role="alert"
          className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm"
        >
          {target.error}
        </p>
      )}
      <Warnings title={t("runs.warnings")} warnings={target.warnings} />
      {ops.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("runs.noPageChanges")}</p>
      ) : (
        <div className="rounded-xl border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("runs.change")}</TableHead>
                <TableHead>{t("runs.page")}</TableHead>
                <TableHead>{t("runs.details")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ops.map((o, i) => (
                <TableRow key={`${o.sourceId}-${i}`}>
                  <TableCell className="whitespace-nowrap">
                    <span className={cn(o.error && "text-destructive")}>
                      {t(`opKind.${o.kind}`)}
                    </span>
                  </TableCell>
                  <TableCell className="font-medium break-all">{o.title}</TableCell>
                  <TableCell className="text-sm break-all text-muted-foreground">
                    {o.error ? (
                      <span className="text-destructive">{o.error}</span>
                    ) : o.from && o.to ? (
                      t("runs.movedFromTo", { from: o.from, to: o.to })
                    ) : (
                      (o.to ?? o.reason ?? "")
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {!!target.attachments?.length && (
        <p className="text-sm text-muted-foreground">
          {t("runs.attachmentsSummary", {
            uploaded: target.attachments.filter((a) => a.kind === "upload").length,
            deleted: target.attachments.filter((a) => a.kind === "delete").length,
          })}
        </p>
      )}
    </section>
  );
}

function Warnings({ title, warnings }: { title: string; warnings?: Schemas["Warning"][] }) {
  if (!warnings?.length) return null;
  return (
    <details className="rounded-lg border border-amber-500/40 bg-amber-500/5 px-4 py-3 text-sm">
      <summary className="cursor-pointer font-medium">
        {title} ({warnings.length})
      </summary>
      <ul className="mt-2 list-disc space-y-1 pl-5">
        {warnings.map((w, i) => (
          <li key={i}>{w.message}</li>
        ))}
      </ul>
    </details>
  );
}

const levelClass: Record<RunEvent["level"], string> = {
  DEBUG: "text-muted-foreground",
  INFO: "",
  WARN: "text-amber-700 dark:text-amber-400",
  ERROR: "text-destructive",
};

function LiveLog({
  events,
  state,
  truncated,
}: {
  events: RunEvent[];
  state: StreamState;
  truncated: boolean;
}) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const box = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);

  // Stay at the bottom while following; scrolling up pauses it.
  useLayoutEffect(() => {
    const el = box.current;
    if (el && follow) el.scrollTop = el.scrollHeight;
  }, [events, follow]);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onScroll = () => setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <div className="space-y-2">
      <p role="status" className="text-sm text-muted-foreground">
        {t(`logState.${state}`)}
        {truncated && ` ${t("runs.logTruncated")}`}
      </p>
      <div
        ref={box}
        // Focusable so keyboard users can scroll the log (axe: scrollable-region-focusable).
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
        tabIndex={0}
        role="log"
        aria-label={t("runs.log")}
        aria-live="off"
        className="h-[32rem] overflow-auto rounded-lg border bg-muted/30 p-3 font-mono text-xs leading-relaxed focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        {events.length === 0 ? (
          <p className="text-muted-foreground">{t("runs.logEmpty")}</p>
        ) : (
          events.map((e) => (
            <div key={e.id} className={cn("break-words whitespace-pre-wrap", levelClass[e.level])}>
              <time dateTime={e.time} className="text-muted-foreground">
                {fmt.dateTime(e.time, { dateStyle: undefined, timeStyle: "medium" })}
              </time>{" "}
              <span className="font-semibold">{e.level.padEnd(5)}</span> {e.msg}
              {Object.entries(e.attrs).map(([k, v]) => (
                <span key={k} className="text-muted-foreground">
                  {" "}
                  {k}={v}
                </span>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
