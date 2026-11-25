import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";

export function NotFoundPage() {
  const { t } = useTranslation();
  return (
    <>
      <PageHeader title={t("notFound.title")} intro={t("notFound.body")} />
      <Button asChild variant="outline">
        <Link to="/">{t("notFound.back")}</Link>
      </Button>
    </>
  );
}
