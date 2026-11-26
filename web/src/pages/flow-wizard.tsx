import { Check, Plus, Trash2 } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams } from "react-router";
import { toast } from "sonner";

import type {
  Config,
  Connection,
  ConnectorKind,
  Flow,
  FlowInput,
  FlowOptions,
  Scope,
} from "@/api/client";
import {
  useConnections,
  useConnectorKinds,
  useFlow,
  usePageTitles,
  useSaveFlow,
  useSchedulePreview,
  useSession,
} from "@/api/queries";
import { Field, FormError } from "@/components/field";
import { PageHeader } from "@/components/page-header";
import { BackLink, QueryError } from "@/components/query-state";
import { ScheduleEditor } from "@/components/schedule-editor";
import { scheduleDraft, scheduleOf, validateSchedule, type ScheduleDraft } from "@/lib/schedule";
import { ScopePicker } from "@/components/scope-picker";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useConnectorText } from "@/lib/connector-text";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { normalize } from "@/lib/scope";
import { cn } from "@/lib/utils";

export const MAX_BANNER = 2000;

const STEPS = ["master", "scope", "mirrors", "options", "schedule", "review"] as const;
type Step = (typeof STEPS)[number];

/** Which step shows the server's error for a field. */
function stepOf(field: string): Step {
  if (field.startsWith("scope")) return "scope";
  if (field.startsWith("targets")) return "mirrors";
  if (field.startsWith("options")) return "options";
  if (field.startsWith("schedule") || field === "timezone") return "schedule";
  return "master";
}

type TargetDraft = {
  key: number;
  id?: string;
  connectionId: string;
  location: Record<string, string>;
};

type Draft = {
  name: string;
  enabled: boolean;
  sourceConnectionId: string;
  scope: Scope;
  options: Required<Omit<FlowOptions, "bannerText">> & { bannerText: string };
  targets: TargetDraft[];
  schedule: ScheduleDraft;
  scheduleEnabled: boolean;
  /** Undefined until edited: a new flow uses the server's timezone. */
  timezone?: string;
};

let nextKey = 1;

function draftFrom(flow?: Flow): Draft {
  if (!flow) {
    return {
      name: "",
      enabled: true,
      sourceConnectionId: "",
      scope: { mode: "all" },
      options: {
        deletePolicy: "archive",
        attachments: true,
        banner: true,
        bannerText: "",
        adoptExisting: false,
      },
      targets: [{ key: nextKey++, connectionId: "", location: {} }],
      schedule: scheduleDraft(),
      scheduleEnabled: true,
    };
  }
  return {
    name: flow.name,
    enabled: flow.enabled,
    sourceConnectionId: flow.sourceConnectionId,
    scope: flow.scope,
    options: {
      deletePolicy: flow.options.deletePolicy ?? "archive",
      attachments: flow.options.attachments ?? true,
      banner: flow.options.banner ?? false,
      bannerText: flow.options.bannerText ?? "",
      adoptExisting: flow.options.adoptExisting ?? false,
    },
    targets: flow.targets.map((m) => ({
      key: nextKey++,
      id: m.id,
      connectionId: m.connectionId,
      location: Object.fromEntries(
        Object.entries((m.location ?? {}) as Config).map(([k, v]) => [k, String(v ?? "")]),
      ),
    })),
    schedule: scheduleDraft(flow.schedule),
    scheduleEnabled: flow.scheduleEnabled,
    timezone: flow.timezone,
  };
}

/** New flow (/flows/new) or edit (/flows/:id/edit). */
export function FlowWizardPage() {
  const { t } = useTranslation();
  const { id } = useParams();
  const flow = useFlow(id);
  const kinds = useConnectorKinds();
  const connections = useConnections();

  const header = (
    <PageHeader
      before={
        <BackLink to={id ? `/flows/${id}` : "/flows"}>
          {id ? (flow.data?.name ?? t("flows.title")) : t("flows.title")}
        </BackLink>
      }
      title={id ? t("flowWizard.editTitle") : t("flowWizard.newTitle")}
      intro={id ? undefined : t("flowWizard.intro")}
    />
  );
  const error = flow.error ?? kinds.error ?? connections.error;
  if (error) {
    return (
      <>
        {header}
        <QueryError error={error} />
      </>
    );
  }
  if ((id && flow.isPending) || kinds.isPending || connections.isPending) {
    return (
      <>
        {header}
        <Skeleton className="h-96" />
      </>
    );
  }
  return (
    <>
      {header}
      <Wizard flow={flow.data} kinds={kinds.data!} connections={connections.data!} />
    </>
  );
}

function Wizard({
  flow,
  kinds,
  connections,
}: {
  flow?: Flow;
  kinds: ConnectorKind[];
  connections: Connection[];
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const save = useSaveFlow(flow?.id);
  const session = useSession();
  const [draft, setDraft] = useState<Draft>(() => draftFrom(flow));
  const [step, setStep] = useState<Step>("master");
  const [errors, setErrors] = useState<Record<string, string>>({});
  // Page titles seen in the scope tree, for the summaries.
  const titles = usePageTitles(draft.sourceConnectionId);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const defaultTimezone = session.data?.timezone ?? "UTC";
  const timezone = draft.timezone ?? defaultTimezone;
  const schedule = scheduleOf(draft.schedule);
  const preview = useSchedulePreview(schedule, timezone);
  const previewErrors = fieldErrors(preview.error);

  const kindOf = useMemo(() => {
    const byKind = new Map(kinds.map((k) => [k.kind, k]));
    return (connectionId: string) =>
      byKind.get(connections.find((c) => c.id === connectionId)?.kind ?? "");
  }, [kinds, connections]);
  const masters = connections.filter((c) => kindOf(c.id)?.canRead);
  const mirrors = connections.filter(
    (c) => kindOf(c.id)?.canWrite && c.id !== draft.sourceConnectionId,
  );
  const nameOf = (connectionId: string) =>
    connections.find((c) => c.id === connectionId)?.name ?? "—";

  const update = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }));
  const updateOptions = (patch: Partial<Draft["options"]>) =>
    setDraft((d) => ({ ...d, options: { ...d.options, ...patch } }));
  const updateTarget = (key: number, patch: Partial<TargetDraft>) =>
    setDraft((d) => ({
      ...d,
      targets: d.targets.map((x) => (x.key === key ? { ...x, ...patch } : x)),
    }));

  // The live preview checks the schedule from here on: drop the old messages.
  const updateSchedule = (patch: Partial<Draft>) => {
    update(patch);
    setErrors((e) =>
      Object.fromEntries(Object.entries(e).filter(([k]) => stepOf(k) !== "schedule")),
    );
  };

  const validateStep = (s: Step): Record<string, string> => {
    const errs: Record<string, string> = {};
    if (s === "master") {
      if (!draft.name.trim()) errs.name = t("validation.required");
      if (!draft.sourceConnectionId) errs.sourceConnectionId = t("flowWizard.masterRequired");
    }
    if (s === "scope" && draft.scope.mode === "selected" && !draft.scope.include?.length) {
      errs["scope.include"] = t("flowWizard.scopeRequired");
    }
    if (s === "mirrors") {
      if (!draft.targets.length) errs.targets = t("flowWizard.mirrorRequired");
      const seen = new Map<string, number>();
      draft.targets.forEach((m, i) => {
        if (!m.connectionId) {
          errs[`targets.${i}.connectionId`] = t("flowWizard.mirrorWikiRequired");
          return;
        }
        if (m.connectionId === draft.sourceConnectionId) {
          errs[`targets.${i}.connectionId`] = t("flowWizard.mirrorIsMaster");
        }
        for (const f of kindOf(m.connectionId)?.locationFields ?? []) {
          if (f.required && !m.location[f.name]?.trim()) {
            errs[`targets.${i}.location.${f.name}`] = t("validation.required");
          }
        }
        const sig = m.connectionId + JSON.stringify(m.location);
        if (seen.has(sig)) {
          errs[`targets.${i}`] = t("flowWizard.mirrorDuplicate", { n: seen.get(sig)! + 1 });
        } else seen.set(sig, i);
      });
    }
    if (s === "options" && draft.options.bannerText.length > MAX_BANNER) {
      errs["options.bannerText"] = t("validation.tooLong", { max: MAX_BANNER });
    }
    if (s === "schedule") Object.assign(errs, validateSchedule(draft.schedule, timezone, t));
    return errs;
  };

  const goTo = (s: Step) => {
    setStep(s);
    // Move focus to the new step for keyboard and screen reader users.
    requestAnimationFrame(() => headingRef.current?.focus());
  };

  const next = () => {
    const errs = validateStep(step);
    setErrors(errs);
    if (Object.keys(errs).length) return;
    goTo(STEPS[STEPS.indexOf(step) + 1]!);
  };

  const input = (): FlowInput => ({
    name: draft.name.trim(),
    enabled: draft.enabled,
    sourceConnectionId: draft.sourceConnectionId,
    scope: normalize(draft.scope),
    options: {
      ...draft.options,
      bannerText: draft.options.bannerText.trim() || undefined,
    },
    schedule,
    scheduleEnabled: draft.scheduleEnabled,
    timezone: timezone.trim(),
    targets: draft.targets.map((m) => {
      const fields = kindOf(m.connectionId)?.locationFields ?? [];
      const location: Config = {};
      for (const f of fields) {
        const v = m.location[f.name]?.trim();
        if (v) location[f.name] = v;
      }
      return { id: m.id, connectionId: m.connectionId, location };
    }),
  });

  const submit = () => {
    for (const s of STEPS) {
      const errs = validateStep(s);
      if (Object.keys(errs).length) {
        setErrors(errs);
        goTo(s);
        return;
      }
    }
    save.mutate(input(), {
      onSuccess: (f) => {
        toast.success(t(flow ? "flowWizard.saved" : "flowWizard.created", { name: f.name }));
        void navigate(`/flows/${f.id}`);
      },
      onError: (err) => {
        const fe = fieldErrors(err);
        setErrors(fe);
        const first = Object.keys(fe)[0];
        if (first) goTo(stepOf(first));
      },
    });
  };

  const formError =
    save.isError && !Object.keys(fieldErrors(save.error)).length
      ? errorMessage(save.error, t)
      : undefined;
  const stepIndex = STEPS.indexOf(step);

  return (
    <div className="grid gap-8 lg:grid-cols-[12rem_1fr]">
      <nav aria-label={t("flowWizard.steps")}>
        <ol className="flex gap-1 overflow-x-auto lg:flex-col">
          {STEPS.map((s, i) => {
            const done = i < stepIndex;
            const hasError = Object.keys(errors).some((k) => stepOf(k) === s);
            return (
              <li key={s}>
                <button
                  type="button"
                  onClick={() => (i <= stepIndex || done ? goTo(s) : next())}
                  aria-current={s === step ? "step" : undefined}
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm whitespace-nowrap hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                    s === step ? "bg-accent font-medium" : "text-muted-foreground",
                    hasError && "text-destructive",
                  )}
                >
                  <span
                    className={cn(
                      "flex size-5 shrink-0 items-center justify-center rounded-full border text-xs",
                      done && "border-primary bg-primary text-primary-foreground",
                    )}
                    aria-hidden="true"
                  >
                    {done ? <Check className="size-3" /> : i + 1}
                  </span>
                  {t(`flowWizard.step.${s}`)}
                </button>
              </li>
            );
          })}
        </ol>
      </nav>

      <section aria-labelledby="step-title" className="max-w-2xl min-w-0 space-y-6">
        <h2
          id="step-title"
          ref={headingRef}
          tabIndex={-1}
          className="text-lg font-medium outline-none"
        >
          {t(`flowWizard.step.${step}`)}
        </h2>
        <FormError>{formError}</FormError>

        {step === "master" && (
          <div className="space-y-5">
            <Field id="flow-name" label={t("common.name")} error={errors.name}>
              {(aria) => (
                <Input
                  {...aria}
                  maxLength={100}
                  value={draft.name}
                  onChange={(e) => update({ name: e.target.value })}
                />
              )}
            </Field>
            <Field
              id="flow-master"
              label={t("flowWizard.master")}
              hint={t("flowWizard.masterHint")}
              error={errors.sourceConnectionId}
            >
              {(aria) => (
                <ConnectionSelect
                  {...aria}
                  value={draft.sourceConnectionId}
                  options={masters}
                  disabled={!!flow}
                  onChange={(v) =>
                    // A new Master has other pages: start the scope over.
                    update({
                      sourceConnectionId: v,
                      scope: { mode: draft.scope.mode },
                      targets: draft.targets.map((m) =>
                        m.connectionId === v ? { ...m, connectionId: "" } : m,
                      ),
                    })
                  }
                />
              )}
            </Field>
            {flow && <p className="text-sm text-muted-foreground">{t("flowWizard.masterFixed")}</p>}
            {masters.length === 0 && <NoConnectionsHint />}
            <div className="flex items-start gap-3">
              <Switch
                id="flow-enabled"
                checked={draft.enabled}
                onCheckedChange={(v) => update({ enabled: v })}
                aria-describedby="flow-enabled-hint"
              />
              <div className="space-y-1">
                <Label htmlFor="flow-enabled">{t("flowWizard.enabled")}</Label>
                <p id="flow-enabled-hint" className="text-sm text-muted-foreground">
                  {t("flowWizard.enabledHint")}
                </p>
              </div>
            </div>
          </div>
        )}

        {step === "scope" && (
          <div className="space-y-5">
            <RadioGroup
              value={draft.scope.mode}
              onValueChange={(v) => update({ scope: { ...draft.scope, mode: v as Scope["mode"] } })}
              aria-label={t("flowWizard.scopeMode")}
              className="space-y-2"
            >
              {(["all", "selected"] as const).map((m) => (
                <div key={m} className="flex items-start gap-3">
                  <RadioGroupItem id={`scope-mode-${m}`} value={m} className="mt-0.5" />
                  <div className="space-y-1">
                    <Label htmlFor={`scope-mode-${m}`}>{t(`flowWizard.scope.${m}`)}</Label>
                    <p className="text-sm text-muted-foreground">
                      {t(`flowWizard.scope.${m}Hint`)}
                    </p>
                  </div>
                </div>
              ))}
            </RadioGroup>
            {errors["scope.include"] && (
              <p role="alert" className="text-sm text-destructive">
                {errors["scope.include"]}
              </p>
            )}
            <ScopePicker
              connectionId={draft.sourceConnectionId}
              scope={draft.scope}
              onChange={(scope) => update({ scope })}
              titles={titles}
            />
          </div>
        )}

        {step === "mirrors" && (
          <div className="space-y-5">
            <p className="text-sm text-muted-foreground">{t("flowWizard.mirrorsHint")}</p>
            {errors.targets && (
              <p role="alert" className="text-sm text-destructive">
                {errors.targets}
              </p>
            )}
            <ol className="space-y-4">
              {draft.targets.map((m, i) => {
                const kind = kindOf(m.connectionId);
                return (
                  <li key={m.key} className="space-y-4 rounded-lg border p-4">
                    <div className="flex items-center justify-between gap-2">
                      <h3 className="font-medium">{t("flowWizard.mirrorN", { n: i + 1 })}</h3>
                      {draft.targets.length > 1 && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          onClick={() =>
                            update({ targets: draft.targets.filter((x) => x.key !== m.key) })
                          }
                        >
                          <Trash2 aria-hidden="true" />
                          {t("flowWizard.removeMirror", { n: i + 1 })}
                        </Button>
                      )}
                    </div>
                    {errors[`targets.${i}`] && (
                      <p className="text-sm text-destructive">{errors[`targets.${i}`]}</p>
                    )}
                    <Field
                      id={`target-${m.key}-wiki`}
                      label={t("flowWizard.mirrorWiki")}
                      error={errors[`targets.${i}.connectionId`]}
                    >
                      {(aria) => (
                        <ConnectionSelect
                          {...aria}
                          value={m.connectionId}
                          options={mirrors}
                          disabled={!!m.id}
                          onChange={(v) => updateTarget(m.key, { connectionId: v, location: {} })}
                        />
                      )}
                    </Field>
                    {kind?.locationFields.map((f) => (
                      <LocationField
                        key={f.name}
                        id={`target-${m.key}-${f.name}`}
                        kind={kind.kind}
                        field={f.name}
                        required={f.required}
                        value={m.location[f.name] ?? ""}
                        error={errors[`targets.${i}.location.${f.name}`]}
                        onChange={(v) =>
                          updateTarget(m.key, { location: { ...m.location, [f.name]: v } })
                        }
                      />
                    ))}
                  </li>
                );
              })}
            </ol>
            <Button
              type="button"
              variant="outline"
              onClick={() =>
                update({
                  targets: [...draft.targets, { key: nextKey++, connectionId: "", location: {} }],
                })
              }
            >
              <Plus aria-hidden="true" />
              {t("flowWizard.addMirror")}
            </Button>
            {mirrors.length === 0 && <NoConnectionsHint />}
          </div>
        )}

        {step === "options" && (
          <div className="space-y-6">
            <fieldset className="space-y-3">
              <legend className="mb-2 font-medium">{t("flowWizard.deletePolicy")}</legend>
              <RadioGroup
                value={draft.options.deletePolicy}
                onValueChange={(v) =>
                  updateOptions({ deletePolicy: v as Draft["options"]["deletePolicy"] })
                }
                className="space-y-2"
              >
                {(["archive", "delete", "keep"] as const).map((p) => (
                  <div key={p} className="flex items-start gap-3">
                    <RadioGroupItem id={`policy-${p}`} value={p} className="mt-0.5" />
                    <div className="space-y-1">
                      <Label htmlFor={`policy-${p}`}>{t(`flowWizard.policy.${p}`)}</Label>
                      <p className="text-sm text-muted-foreground">
                        {t(`flowWizard.policy.${p}Hint`)}
                      </p>
                    </div>
                  </div>
                ))}
              </RadioGroup>
            </fieldset>
            <OptionSwitch
              id="opt-attachments"
              label={t("flowWizard.attachments")}
              hint={t("flowWizard.attachmentsHint")}
              checked={draft.options.attachments}
              onChange={(v) => updateOptions({ attachments: v })}
            />
            <OptionSwitch
              id="opt-banner"
              label={t("flowWizard.banner")}
              hint={t("flowWizard.bannerHint")}
              checked={draft.options.banner}
              onChange={(v) => updateOptions({ banner: v })}
            />
            {draft.options.banner && (
              <Field
                id="opt-banner-text"
                className="pl-12"
                label={
                  <>
                    {t("flowWizard.bannerText")}{" "}
                    <span className="font-normal text-muted-foreground">
                      ({t("common.optional")})
                    </span>
                  </>
                }
                hint={t("flowWizard.bannerTextHint")}
                error={errors["options.bannerText"]}
              >
                {(aria) => (
                  <Textarea
                    {...aria}
                    rows={3}
                    maxLength={MAX_BANNER}
                    placeholder={t("flowWizard.bannerDefault", {
                      master: nameOf(draft.sourceConnectionId),
                    })}
                    value={draft.options.bannerText}
                    onChange={(e) => updateOptions({ bannerText: e.target.value })}
                  />
                )}
              </Field>
            )}
            <OptionSwitch
              id="opt-adopt"
              label={t("flowWizard.adopt")}
              hint={t("flowWizard.adoptHint")}
              checked={draft.options.adoptExisting}
              onChange={(v) => updateOptions({ adoptExisting: v })}
            />
          </div>
        )}

        {step === "schedule" && (
          <ScheduleEditor
            value={draft.schedule}
            onChange={(d) => updateSchedule({ schedule: d })}
            timezone={timezone}
            defaultTimezone={defaultTimezone}
            onTimezoneChange={(tz) => updateSchedule({ timezone: tz })}
            builtIn={draft.scheduleEnabled}
            onBuiltInChange={(v) => update({ scheduleEnabled: v })}
            preview={preview.isError ? undefined : preview.data}
            previewPending={preview.isFetching}
            // The live preview's messages, until the user saves.
            errors={{ ...previewErrors, ...errors }}
          />
        )}

        {step === "review" && (
          <dl className="divide-y rounded-lg border text-sm">
            <ReviewRow label={t("common.name")} onEdit={() => goTo("master")}>
              {draft.name}
              {!draft.enabled && ` (${t("flows.disabled")})`}
            </ReviewRow>
            <ReviewRow label={t("flowWizard.master")} onEdit={() => goTo("master")}>
              {nameOf(draft.sourceConnectionId)}
            </ReviewRow>
            <ReviewRow label={t("flowWizard.step.scope")} onEdit={() => goTo("scope")}>
              {draft.scope.mode === "all"
                ? t("flowWizard.scope.all")
                : (draft.scope.include ?? []).map((id) => titles.get(id) ?? id).join(", ")}
              {!!draft.scope.exclude?.length && (
                <span className="block text-muted-foreground">
                  {t("flowWizard.excludedList", {
                    list: draft.scope.exclude.map((id) => titles.get(id) ?? id).join(", "),
                  })}
                </span>
              )}
            </ReviewRow>
            <ReviewRow label={t("flowWizard.step.mirrors")} onEdit={() => goTo("mirrors")}>
              <ul>
                {draft.targets.map((m) => (
                  <li key={m.key}>
                    {nameOf(m.connectionId)}
                    {Object.values(m.location).some(Boolean) && (
                      <span className="text-muted-foreground">
                        {" "}
                        — {Object.values(m.location).filter(Boolean).join(", ")}
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </ReviewRow>
            <ReviewRow label={t("flowWizard.step.options")} onEdit={() => goTo("options")}>
              <ul>
                <li>{t(`flowWizard.policy.${draft.options.deletePolicy}`)}</li>
                <li>
                  {draft.options.attachments
                    ? t("flowWizard.attachmentsOn")
                    : t("flowWizard.attachmentsOff")}
                </li>
                <li>
                  {draft.options.banner ? t("flowWizard.bannerOn") : t("flowWizard.bannerOff")}
                </li>
                {draft.options.adoptExisting && <li>{t("flowWizard.adoptOn")}</li>}
              </ul>
            </ReviewRow>
            <ReviewRow label={t("flowWizard.step.schedule")} onEdit={() => goTo("schedule")}>
              {preview.data?.description ?? "…"}
              {schedule.kind !== "manual" && !draft.scheduleEnabled && (
                <span className="block text-muted-foreground">{t("schedule.builtInOff")}</span>
              )}
            </ReviewRow>
          </dl>
        )}

        <div className="flex flex-wrap gap-2 border-t pt-6">
          {stepIndex > 0 && (
            <Button type="button" variant="outline" onClick={() => goTo(STEPS[stepIndex - 1]!)}>
              {t("common.back")}
            </Button>
          )}
          {step !== "review" ? (
            <Button type="button" onClick={next}>
              {t("common.next")}
            </Button>
          ) : (
            <Button type="button" onClick={submit} disabled={save.isPending}>
              {flow ? t("common.save") : t("flowWizard.create")}
            </Button>
          )}
          <Button type="button" variant="ghost" asChild>
            <Link to={flow ? `/flows/${flow.id}` : "/flows"}>{t("common.cancel")}</Link>
          </Button>
        </div>
      </section>
    </div>
  );
}

function ConnectionSelect({
  value,
  options,
  disabled,
  onChange,
  ...aria
}: {
  id: string;
  value: string;
  options: Connection[];
  disabled?: boolean;
  onChange: (v: string) => void;
  "aria-invalid": boolean | undefined;
  "aria-describedby": string | undefined;
}) {
  const { t } = useTranslation();
  return (
    <Select value={value || undefined} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger {...aria} className="w-full">
        <SelectValue placeholder={t("flowWizard.chooseWiki")} />
      </SelectTrigger>
      <SelectContent>
        {options.map((c) => (
          <SelectItem key={c.id} value={c.id}>
            {c.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function LocationField({
  id,
  kind,
  field,
  required,
  value,
  error,
  onChange,
}: {
  id: string;
  kind: string;
  field: string;
  required: boolean;
  value: string;
  error?: string;
  onChange: (v: string) => void;
}) {
  const { t } = useTranslation();
  const text = useConnectorText();
  return (
    <Field
      id={id}
      label={
        <>
          {text.label(kind, `location.${field}`)}
          {!required && (
            <span className="font-normal text-muted-foreground"> ({t("common.optional")})</span>
          )}
        </>
      }
      hint={text.hint(kind, `location.${field}`)}
      error={error}
    >
      {(aria) => (
        <Input
          {...aria}
          spellCheck={false}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </Field>
  );
}

function OptionSwitch({
  id,
  label,
  hint,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  hint: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-start gap-3">
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onChange}
        aria-describedby={`${id}-hint`}
        className="mt-0.5"
      />
      <div className="space-y-1">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-sm text-muted-foreground">
          {hint}
        </p>
      </div>
    </div>
  );
}

function ReviewRow({
  label,
  onEdit,
  children,
}: {
  label: string;
  onEdit: () => void;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="grid gap-1 p-4 sm:grid-cols-[10rem_1fr_auto] sm:gap-4">
      <dt className="font-medium">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
      <dd>
        <Button type="button" variant="link" size="sm" className="h-auto p-0" onClick={onEdit}>
          {t("common.edit")}
          <span className="sr-only"> {label}</span>
        </Button>
      </dd>
    </div>
  );
}

function NoConnectionsHint() {
  const { t } = useTranslation();
  return (
    <p className="text-sm text-muted-foreground">
      {t("flowWizard.noConnections")}{" "}
      <Link to="/connections/new" className="underline underline-offset-4">
        {t("connections.new")}
      </Link>
    </p>
  );
}
