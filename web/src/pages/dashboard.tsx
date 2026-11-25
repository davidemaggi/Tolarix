import { Workflow } from "lucide-react";
import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/page-header";

export function DashboardPage() {
  const { t } = useTranslation();
  return (
    <>
      <PageHeader title={t("dashboard.title")} intro={t("dashboard.intro")} />
      <section
        aria-labelledby="empty-flows"
        className="flex flex-col items-center gap-3 rounded-xl border border-dashed px-6 py-16 text-center"
      >
        <Workflow className="size-10 text-muted-foreground" aria-hidden="true" />
        <h2 id="empty-flows" className="text-lg font-medium">
          {t("dashboard.emptyTitle")}
        </h2>
        <p className="max-w-md text-sm text-muted-foreground">{t("dashboard.emptyBody")}</p>
      </section>
    </>
  );
}
