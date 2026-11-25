import { ArrowLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { ApiError } from "@/api/client";
import { Button } from "@/components/ui/button";
import { errorMessage } from "@/lib/errors";

/** A failed load, with a retry button (or "not found" for a 404). */
export function QueryError({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation();
  const notFound = error instanceof ApiError && error.status === 404;
  return (
    <div role="alert" className="space-y-3 rounded-xl border px-5 py-6">
      <p className="font-medium">{notFound ? t("errors.notFound") : t("errors.loadFailed")}</p>
      {!notFound && <p className="text-sm text-muted-foreground">{errorMessage(error, t)}</p>}
      {onRetry && !notFound && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}

export function BackLink({ to, children }: { to: string; children: React.ReactNode }) {
  return (
    <Link
      to={to}
      className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-4" aria-hidden="true" />
      {children}
    </Link>
  );
}
