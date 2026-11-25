import { useTranslation } from "react-i18next";

import { PageHeader } from "@/components/page-header";

type Section = "connections" | "flows" | "settings";

/** Temporary page for sections implemented in later milestones. */
export function PlaceholderPage({ section }: { section: Section }) {
  const { t } = useTranslation();
  return (
    <>
      <PageHeader title={t(`${section}.title`)} intro={t(`${section}.intro`)} />
      <p className="text-sm text-muted-foreground">{t("comingSoon")}</p>
    </>
  );
}
