import { useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { onAuthProblem } from "@/api/client";
import { keys, useLogin, useSession } from "@/api/queries";
import { ChangePasswordForm } from "@/components/change-password-form";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";

/**
 * Shows the app only to a signed-in admin (web mode). Desktop mode has no
 * sign-in. A one-time password must be changed before anything else.
 */
export function SessionGate({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const session = useSession();

  useEffect(() => {
    onAuthProblem(() => void qc.invalidateQueries({ queryKey: keys.session }));
    return () => onAuthProblem(undefined);
  }, [qc]);

  if (session.isPending) {
    return (
      <Centered>
        <Loader2 className="size-6 animate-spin text-muted-foreground" aria-hidden="true" />
        <span className="sr-only">{t("common.loading")}</span>
      </Centered>
    );
  }
  if (session.isError) {
    return (
      <Centered>
        <Card className="w-full max-w-sm">
          <CardHeader>
            <CardTitle>{t("auth.unreachableTitle")}</CardTitle>
            <CardDescription>{t("auth.unreachableBody")}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void session.refetch()}>{t("common.retry")}</Button>
          </CardContent>
        </Card>
      </Centered>
    );
  }
  const s = session.data;
  if (s.mode === "web" && !s.authenticated) return <LoginScreen />;
  if (s.mode === "web" && s.mustChangePassword) return <ForcedPasswordChange />;
  return <>{children}</>;
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-svh flex-col items-center justify-center gap-6 p-4">
      {children}
    </main>
  );
}

function Brand() {
  const { t } = useTranslation();
  return (
    <div className="flex items-center gap-2 text-lg font-semibold">
      <img src="/favicon.svg" alt="" className="size-7" />
      <span>{t("app.name")}</span>
    </div>
  );
}

function LoginScreen() {
  const { t } = useTranslation();
  const login = useLogin();
  const [password, setPassword] = useState("");
  const [missing, setMissing] = useState(false);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setMissing(!password);
    if (password) login.mutate(password, { onError: () => setPassword("") });
  };

  return (
    <Centered>
      <Brand />
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>
            <h1>{t("auth.signInTitle")}</h1>
          </CardTitle>
          <CardDescription>{t("auth.signInBody")}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} noValidate className="space-y-4">
            <FormError>{login.isError ? errorMessage(login.error, t) : undefined}</FormError>
            <Field
              id="password"
              label={t("auth.password")}
              error={missing ? t("auth.passwordRequired") : undefined}
            >
              {(aria) => (
                <Input
                  {...aria}
                  type="password"
                  autoComplete="current-password"
                  // eslint-disable-next-line jsx-a11y/no-autofocus -- the only field on the page
                  autoFocus
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              )}
            </Field>
            <Button type="submit" className="w-full" disabled={login.isPending}>
              {t("auth.signIn")}
            </Button>
          </form>
          <p className="mt-4 text-sm text-muted-foreground">{t("auth.forgotHint")}</p>
        </CardContent>
      </Card>
    </Centered>
  );
}

function ForcedPasswordChange() {
  const { t } = useTranslation();
  return (
    <Centered>
      <Brand />
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>
            <h1>{t("auth.choosePasswordTitle")}</h1>
          </CardTitle>
          <CardDescription>{t("auth.choosePasswordBody")}</CardDescription>
        </CardHeader>
        <CardContent>
          <ChangePasswordForm submitLabel={t("auth.choosePasswordSubmit")} />
        </CardContent>
      </Card>
    </Centered>
  );
}
