import { FlaskConical, Play } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { toast } from "sonner";

import type { Flow } from "@/api/client";
import { useStartRun } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/errors";

/** "Run now" and "Dry-run" for a flow; opens the run's live log. */
export function RunButtons({ flow, size = "sm" }: { flow: Flow; size?: "sm" | "default" }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const start = useStartRun();
  const busy = !!flow.running || start.isPending;

  const run = (dryRun: boolean) =>
    start.mutate(
      { flowId: flow.id, dryRun },
      {
        onSuccess: (r) => void navigate(`/runs/${r.id}`),
        onError: (err) => toast.error(errorMessage(err, t)),
      },
    );

  return (
    <div className="flex flex-wrap gap-2">
      <Button
        size={size}
        onClick={() => run(false)}
        disabled={busy}
        aria-label={t("flows.runNowFor", { name: flow.name })}
      >
        <Play aria-hidden="true" />
        {t("flows.runNow")}
      </Button>
      <Button
        size={size}
        variant="outline"
        onClick={() => run(true)}
        disabled={busy}
        aria-label={t("flows.dryRunFor", { name: flow.name })}
        title={t("flows.dryRunHint")}
      >
        <FlaskConical aria-hidden="true" />
        {t("flows.dryRun")}
      </Button>
    </div>
  );
}
