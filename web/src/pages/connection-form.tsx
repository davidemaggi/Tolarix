import { CheckCircle2, Loader2, XCircle } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import type { Config, Connection, ConnectorField, ConnectorKind } from "@/api/client";
import {
  useConnection,
  useConnectorKinds,
  useSaveConnection,
  useTestConnection,
} from "@/api/queries";
import { Field, FormError } from "@/components/field";
import { PageHeader } from "@/components/page-header";
import { BackLink, QueryError } from "@/components/query-state";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useConnectorText } from "@/lib/connector-text";
import { errorMessage, fieldErrors } from "@/lib/errors";

/** New connection (/connections/new) or edit (/connections/:id). */
export function ConnectionFormPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const kinds = useConnectorKinds();
  const existing = useConnection(id);

  const header = (
    <PageHeader
      before={<BackLink to="/connections">{t("connections.title")}</BackLink>}
      title={id ? (existing.data?.name ?? t("connections.edit")) : t("connections.new")}
      intro={id ? undefined : t("connections.newIntro")}
    />
  );

  if (kinds.isPending || (id && existing.isPending)) {
    return (
      <>
        {header}
        <Skeleton className="h-64" />
      </>
    );
  }
  if (kinds.isError || existing.isError) {
    return (
      <>
        {header}
        <QueryError error={kinds.error ?? existing.error} />
      </>
    );
  }
  return (
    <>
      {header}
      <ConnectionForm kinds={kinds.data} existing={existing.data} />
    </>
  );
}

type Values = Record<string, string | boolean>;

function initialValues(kind: ConnectorKind | undefined, existing?: Connection): Values {
  const out: Values = {};
  const cfg = (existing?.config ?? {}) as Config;
  for (const f of kind?.fields ?? []) {
    if (f.secret || f.type === "secret") continue;
    const v = cfg[f.name];
    out[f.name] = f.type === "bool" ? v === true : typeof v === "string" ? v : "";
  }
  return out;
}

function isSecret(f: ConnectorField) {
  return f.secret || f.type === "secret";
}

function ConnectionForm({ kinds, existing }: { kinds: ConnectorKind[]; existing?: Connection }) {
  const { t } = useTranslation();
  const text = useConnectorText();
  const navigate = useNavigate();
  const save = useSaveConnection(existing?.id);
  const test = useTestConnection();

  const [kindName, setKindName] = useState(existing?.kind ?? "");
  const kind = kinds.find((k) => k.kind === kindName);
  const [name, setName] = useState(existing?.name ?? "");
  const [values, setValues] = useState<Values>(() => initialValues(kind, existing));
  // Saved connections keep their secret unless the user replaces or removes it.
  const [secretMode, setSecretMode] = useState<"keep" | "replace" | "remove">(
    existing?.hasSecret ? "keep" : "replace",
  );
  const [secret, setSecret] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  // Picking another type starts its settings from scratch.
  const pickKind = (next: string) => {
    if (next === kindName) return;
    setKindName(next);
    setValues(initialValues(kinds.find((k) => k.kind === next)));
    test.reset();
  };

  const secretField = kind?.fields.find(isSecret);
  const plainFields = kind?.fields.filter((f) => !isSecret(f)) ?? [];

  const config = (): Config => {
    const out: Config = {};
    for (const f of plainFields) {
      const v = values[f.name];
      if (typeof v === "string") {
        if (v.trim() !== "") out[f.name] = v.trim();
      } else if (v !== undefined) out[f.name] = v;
    }
    return out;
  };

  const secretValue = (): string | null | undefined => {
    if (!secretField) return undefined;
    if (secretMode === "keep") return undefined;
    if (secretMode === "remove") return "";
    return secret;
  };

  const validate = () => {
    const errs: Record<string, string> = {};
    if (!kind) errs.kind = t("connections.kindRequired");
    if (!name.trim()) errs.name = t("validation.required");
    for (const f of plainFields) {
      if (f.required && f.type !== "bool" && !String(values[f.name] ?? "").trim()) {
        errs[`config.${f.name}`] = t("validation.required");
      }
    }
    if (secretField?.required && secretMode !== "keep" && !secret) {
      errs.secret = t("validation.required");
    }
    setErrors(errs);
    return Object.keys(errs).length === 0;
  };

  const onTest = () => {
    if (!kind) return;
    test.mutate({
      kind: kind.kind,
      config: config(),
      secret: secretValue(),
      connectionId: secretMode === "keep" ? existing?.id : undefined,
    });
  };

  const onSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!validate() || !kind) return;
    save.mutate(
      { name: name.trim(), kind: kind.kind, config: config(), secret: secretValue() },
      {
        onSuccess: (c) => {
          toast.success(
            t(existing ? "connections.saved" : "connections.created", { name: c.name }),
          );
          void navigate("/connections");
        },
        onError: (err) => setErrors(fieldErrors(err)),
      },
    );
  };

  const formError =
    save.isError && !Object.keys(fieldErrors(save.error)).length
      ? errorMessage(save.error, t)
      : undefined;

  return (
    <form onSubmit={onSubmit} noValidate className="max-w-2xl space-y-8">
      <FormError>{formError}</FormError>

      <fieldset className="space-y-3">
        <legend className="mb-3 text-lg font-medium">{t("connections.stepKind")}</legend>
        {existing ? (
          <p className="text-sm">
            <span className="font-medium">{kind?.displayName ?? existing.kind}</span>{" "}
            <span className="text-muted-foreground">— {t("connections.kindFixed")}</span>
          </p>
        ) : (
          <RadioGroup
            value={kindName}
            onValueChange={pickKind}
            className="grid gap-3 sm:grid-cols-2"
            aria-invalid={errors.kind ? true : undefined}
            aria-describedby={errors.kind ? "kind-error" : undefined}
          >
            {kinds.map((k) => (
              <Label
                key={k.kind}
                htmlFor={`kind-${k.kind}`}
                className="flex cursor-pointer items-start gap-3 rounded-lg border p-4 font-normal has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-accent"
              >
                <RadioGroupItem id={`kind-${k.kind}`} value={k.kind} className="mt-0.5" />
                <span className="space-y-1">
                  <span className="block font-medium">{k.displayName}</span>
                  {text.kindDescription(k.kind) && (
                    <span className="block text-sm text-muted-foreground">
                      {text.kindDescription(k.kind)}
                    </span>
                  )}
                  <span className="block text-xs text-muted-foreground">
                    {[
                      k.canRead && t("connections.canMaster"),
                      k.canWrite && t("connections.canMirror"),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </span>
              </Label>
            ))}
          </RadioGroup>
        )}
        {errors.kind && (
          <p id="kind-error" className="text-sm text-destructive">
            {errors.kind}
          </p>
        )}
      </fieldset>

      {kind && (
        <fieldset className="space-y-5">
          <legend className="mb-3 text-lg font-medium">{t("connections.stepDetails")}</legend>
          <Field
            id="conn-name"
            label={t("common.name")}
            hint={t("connections.nameHint")}
            error={errors.name}
          >
            {(aria) => (
              <Input
                {...aria}
                maxLength={100}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            )}
          </Field>

          {plainFields.map((f) =>
            f.type === "bool" ? (
              <div key={f.name} className="flex items-center gap-3">
                <Switch
                  id={`cfg-${f.name}`}
                  checked={values[f.name] === true}
                  onCheckedChange={(v) => setValues((s) => ({ ...s, [f.name]: v }))}
                />
                <Label htmlFor={`cfg-${f.name}`}>{text.label(kind.kind, f.name)}</Label>
              </div>
            ) : (
              <Field
                key={f.name}
                id={`cfg-${f.name}`}
                label={
                  <>
                    {text.label(kind.kind, f.name)}
                    {!f.required && (
                      <span className="font-normal text-muted-foreground">
                        {" "}
                        ({t("common.optional")})
                      </span>
                    )}
                  </>
                }
                hint={text.hint(kind.kind, f.name)}
                error={errors[`config.${f.name}`]}
              >
                {(aria) => (
                  <Input
                    {...aria}
                    type={f.type === "url" ? "url" : "text"}
                    spellCheck={false}
                    autoComplete="off"
                    value={String(values[f.name] ?? "")}
                    onChange={(e) => setValues((s) => ({ ...s, [f.name]: e.target.value }))}
                  />
                )}
              </Field>
            ),
          )}

          {secretField && (
            <div className="space-y-2">
              {existing?.hasSecret && secretMode !== "replace" ? (
                <>
                  <p className="text-sm font-medium">{text.label(kind.kind, secretField.name)}</p>
                  <p className="text-sm text-muted-foreground">
                    {secretMode === "keep"
                      ? t("connections.secretStored")
                      : t("connections.secretWillBeRemoved")}
                  </p>
                  <div className="flex gap-2">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => setSecretMode("replace")}
                    >
                      {t("connections.secretReplace")}
                    </Button>
                    {!secretField.required && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => setSecretMode(secretMode === "keep" ? "remove" : "keep")}
                      >
                        {secretMode === "keep"
                          ? t("connections.secretRemove")
                          : t("connections.secretKeep")}
                      </Button>
                    )}
                  </div>
                </>
              ) : (
                <Field
                  id="cfg-secret"
                  label={text.label(kind.kind, secretField.name)}
                  hint={text.hint(kind.kind, secretField.name) ?? t("connections.secretHint")}
                  error={errors.secret}
                >
                  {(aria) => (
                    <Input
                      {...aria}
                      type="password"
                      autoComplete="new-password"
                      value={secret}
                      onChange={(e) => setSecret(e.target.value)}
                    />
                  )}
                </Field>
              )}
            </div>
          )}

          <div className="space-y-2">
            <Button type="button" variant="outline" onClick={onTest} disabled={test.isPending}>
              {test.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
              {t("connections.test")}
            </Button>
            <div role="status" className="text-sm">
              {test.data &&
                (test.data.ok ? (
                  <p className="flex items-start gap-2 text-emerald-700 dark:text-emerald-400">
                    <CheckCircle2 className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                    <span>
                      {t("connections.testOk")}
                    </span>
                  </p>
                ) : (
                  <p className="flex items-start gap-2 text-destructive">
                    <XCircle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                    <span>
                      {t("connections.testFailed")} {test.data.message}
                    </span>
                  </p>
                ))}
              {test.isError && <p className="text-destructive">{errorMessage(test.error, t)}</p>}
            </div>
          </div>
        </fieldset>
      )}

      <div className="flex gap-2 border-t pt-6">
        <Button type="submit" disabled={save.isPending}>
          {existing ? t("common.save") : t("connections.create")}
        </Button>
        <Button type="button" variant="ghost" onClick={() => void navigate("/connections")}>
          {t("common.cancel")}
        </Button>
      </div>
    </form>
  );
}
