import { CheckCircle2, CircleSlash, Loader2, TriangleAlert, XCircle } from "lucide-react";
import { useTranslation } from "react-i18next";

import type { Counts, RunStatus } from "@/api/client";
import { cn } from "@/lib/utils";

const styles: Record<RunStatus, { icon: typeof CheckCircle2; className: string }> = {
  running: { icon: Loader2, className: "text-sky-700 bg-sky-500/10 dark:text-sky-300" },
  succeeded: {
    icon: CheckCircle2,
    className: "text-emerald-700 bg-emerald-500/10 dark:text-emerald-300",
  },
  partial: {
    icon: TriangleAlert,
    className: "text-amber-800 bg-amber-500/15 dark:text-amber-300",
  },
  failed: { icon: XCircle, className: "text-destructive bg-destructive/10" },
  cancelled: { icon: CircleSlash, className: "text-muted-foreground bg-muted" },
};

export function RunStatusBadge({ status, dryRun }: { status: RunStatus; dryRun?: boolean }) {
  const { t } = useTranslation();
  const { icon: Icon, className } = styles[status];
  return (
    <span className="inline-flex items-center gap-1.5">
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium",
          className,
        )}
      >
        <Icon
          className={cn("size-3.5", status === "running" && "animate-spin")}
          aria-hidden="true"
        />
        {t(`runStatus.${status}`)}
      </span>
      {dryRun && (
        <span className="rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
          {t("runs.dryRun")}
        </span>
      )}
    </span>
  );
}

const countKeys = [
  "created",
  "updated",
  "moved",
  "deleted",
  "archived",
  "failed",
] as const satisfies readonly (keyof Counts)[];

/** "3 created · 1 updated", or "No changes". Only non-zero counts are listed. */
export function CountsSummary({ counts, className }: { counts: Counts; className?: string }) {
  const { t } = useTranslation();
  const parts = countKeys.filter((k) => counts[k] > 0);
  if (!parts.length) {
    return <span className={cn("text-muted-foreground", className)}>{t("counts.none")}</span>;
  }
  return (
    <span className={className}>
      {parts.map((k, i) => (
        <span key={k} className={cn(k === "failed" && "font-medium text-destructive")}>
          {i > 0 && " · "}
          {t(`counts.${k}`, { count: counts[k] })}
        </span>
      ))}
    </span>
  );
}
