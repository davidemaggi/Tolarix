import { LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { useLogout, useSession } from "@/api/queries";
import { ChangePasswordForm } from "@/components/change-password-form";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";

export function SettingsPage() {
  const { t } = useTranslation();
  const session = useSession();
  const logout = useLogout();
  const web = session.data?.mode === "web";

  return (
    <>
      <PageHeader title={t("settings.title")} intro={t("settings.intro")} />
      <div className="max-w-xl space-y-10">
        <section aria-labelledby="about" className="space-y-2">
          <h2 id="about" className="text-lg font-medium">
            {t("settings.about")}
          </h2>
          <dl className="grid grid-cols-[10rem_1fr] gap-y-1 text-sm">
            <dt className="text-muted-foreground">{t("settings.version")}</dt>
            <dd>{session.data?.version}</dd>
            <dt className="text-muted-foreground">{t("settings.timezone")}</dt>
            <dd>{session.data?.timezone}</dd>
          </dl>
          <p className="text-sm text-muted-foreground">{t("settings.appearanceHint")}</p>
        </section>

        {web && (
          <section aria-labelledby="account" className="space-y-4">
            <h2 id="account" className="text-lg font-medium">
              {t("settings.account")}
            </h2>
            <h3 className="font-medium">{t("settings.changePassword")}</h3>
            <p className="text-sm text-muted-foreground">{t("settings.changePasswordHint")}</p>
            <ChangePasswordForm onDone={() => toast.success(t("settings.passwordChanged"))} />
            <div className="border-t pt-6">
              <Button variant="outline" onClick={() => logout.mutate()} disabled={logout.isPending}>
                <LogOut aria-hidden="true" />
                {t("auth.signOut")}
              </Button>
            </div>
          </section>
        )}
      </div>
    </>
  );
}
