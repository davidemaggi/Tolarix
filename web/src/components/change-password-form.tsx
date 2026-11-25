import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useChangePassword } from "@/api/queries";
import { Field, FormError } from "@/components/field";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { errorMessage, fieldErrors } from "@/lib/errors";

export const MIN_PASSWORD = 12;

export function ChangePasswordForm({
  onDone,
  submitLabel,
}: {
  onDone?: () => void;
  submitLabel?: string;
}) {
  const { t } = useTranslation();
  const change = useChangePassword();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const errs: Record<string, string> = {};
    if (!current) errs.currentPassword = t("password.currentRequired");
    if (next.length < MIN_PASSWORD)
      errs.newPassword = t("password.tooShort", { min: MIN_PASSWORD });
    else if (next === current) errs.newPassword = t("password.same");
    if (confirm !== next) errs.confirm = t("password.mismatch");
    setErrors(errs);
    if (Object.keys(errs).length) return;
    change.mutate(
      { currentPassword: current, newPassword: next },
      {
        onSuccess: () => {
          setCurrent("");
          setNext("");
          setConfirm("");
          onDone?.();
        },
        onError: (err) => setErrors(fieldErrors(err)),
      },
    );
  };

  const formError =
    change.isError && !Object.keys(fieldErrors(change.error)).length
      ? errorMessage(change.error, t)
      : undefined;

  return (
    <form onSubmit={submit} noValidate className="space-y-4">
      <FormError>{formError}</FormError>
      <Field id="current-password" label={t("password.current")} error={errors.currentPassword}>
        {(aria) => (
          <Input
            {...aria}
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        )}
      </Field>
      <Field
        id="new-password"
        label={t("password.new")}
        hint={t("password.hint", { min: MIN_PASSWORD })}
        error={errors.newPassword}
      >
        {(aria) => (
          <Input
            {...aria}
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        )}
      </Field>
      <Field id="confirm-password" label={t("password.confirm")} error={errors.confirm}>
        {(aria) => (
          <Input
            {...aria}
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
        )}
      </Field>
      <Button type="submit" disabled={change.isPending}>
        {submitLabel ?? t("password.submit")}
      </Button>
    </form>
  );
}
