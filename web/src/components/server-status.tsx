import { useTranslation } from "react-i18next";

import { useHealth } from "@/api/health";
import { cn } from "@/lib/utils";

export function ServerStatus() {
  const { t } = useTranslation();
  const { data, isPending, isError } = useHealth();
  const ok = !isError && data?.status === "ok";
  const label = isPending
    ? t("status.checking")
    : ok
      ? t("status.ok", { version: data.version })
      : t("status.unreachable");
  return (
    <p className="flex items-center gap-2 text-xs text-muted-foreground" role="status">
      <span
        aria-hidden="true"
        className={cn(
          "size-2 rounded-full",
          isPending ? "bg-muted-foreground/50" : ok ? "bg-emerald-500" : "bg-destructive",
        )}
      />
      <span>
        {t("status.server")}: {label}
      </span>
    </p>
  );
}
