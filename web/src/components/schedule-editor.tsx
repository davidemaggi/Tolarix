import { useMemo } from "react";
import { useTranslation } from "react-i18next";

import type { SchedulePreview } from "@/api/client";
import { Field } from "@/components/field";
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
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useFormatters } from "@/lib/format";
import { STEPS, WEEKDAYS, type ScheduleDraft, type SimpleKind, type Unit } from "@/lib/schedule";
import { cn } from "@/lib/utils";

function timezones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: string) => string[] };
  const list = intl.supportedValuesOf?.("timeZone") ?? [];
  return list.includes("UTC") ? list : ["UTC", ...list];
}

/** Weekday names in the UI language, Monday first. */
function useWeekdayNames(style: "short" | "long") {
  const { locale } = useFormatters();
  return useMemo(() => {
    const f = new Intl.DateTimeFormat(locale, { weekday: style, timeZone: "UTC" });
    // 1 January 2024 was a Monday.
    return WEEKDAYS.map((_, i) => f.format(new Date(Date.UTC(2024, 0, 1 + i))));
  }, [locale, style]);
}

export function ScheduleEditor({
  value,
  onChange,
  timezone,
  defaultTimezone,
  onTimezoneChange,
  builtIn,
  onBuiltInChange,
  preview,
  previewPending,
  errors,
}: {
  value: ScheduleDraft;
  onChange: (d: ScheduleDraft) => void;
  timezone: string;
  defaultTimezone: string;
  onTimezoneChange: (tz: string) => void;
  builtIn: boolean;
  onBuiltInChange: (v: boolean) => void;
  preview?: SchedulePreview;
  previewPending: boolean;
  /** Field errors keyed like the server's: "schedule.time", "timezone", ... */
  errors: Record<string, string>;
}) {
  const { t } = useTranslation();
  const fmt = useFormatters();
  const shortDays = useWeekdayNames("short");
  const longDays = useWeekdayNames("long");
  const zones = useMemo(() => timezones(), []);
  const set = (patch: Partial<ScheduleDraft>) => onChange({ ...value, ...patch });
  const manual = value.tab === "simple" && value.kind === "manual";
  const scheduleError = errors.schedule ?? errors["schedule.kind"];

  const nextRun = (iso: string) => {
    try {
      return fmt.dateTime(iso, {
        dateStyle: "full",
        timeStyle: "short",
        timeZone: preview?.timezone,
      });
    } catch {
      return fmt.dateTime(iso, { dateStyle: "full", timeStyle: "short" });
    }
  };

  const timeField = (
    <Field id="schedule-time" label={t("schedule.time")} error={errors["schedule.time"]}>
      {(aria) => (
        <Input
          {...aria}
          type="time"
          className="w-36"
          value={value.time}
          onChange={(e) => set({ time: e.target.value })}
        />
      )}
    </Field>
  );

  return (
    <div className="space-y-6">
      <Tabs
        value={value.tab}
        onValueChange={(tab) =>
          set({
            tab: tab as ScheduleDraft["tab"],
            // Start the expression from what the simple form describes.
            expr: tab === "advanced" && !value.expr.trim() ? (preview?.cron ?? "") : value.expr,
          })
        }
      >
        <TabsList aria-label={t("schedule.tabs")}>
          <TabsTrigger value="simple">{t("schedule.simple")}</TabsTrigger>
          <TabsTrigger value="advanced">{t("schedule.advanced")}</TabsTrigger>
        </TabsList>

        <TabsContent value="simple" className="pt-3">
          <RadioGroup
            value={value.kind}
            onValueChange={(k) => set({ kind: k as SimpleKind })}
            aria-label={t("schedule.kindLabel")}
            className="space-y-3"
          >
            {(["manual", "interval", "daily", "weekly", "monthly"] as const).map((k) => (
              <div key={k} className="space-y-3">
                <div className="flex items-center gap-3">
                  <RadioGroupItem id={`schedule-kind-${k}`} value={k} />
                  <Label htmlFor={`schedule-kind-${k}`} className="font-normal">
                    {t(`schedule.kind.${k}`)}
                  </Label>
                </div>
                {value.kind === k && k !== "manual" && (
                  <div className="ml-7 flex flex-wrap items-start gap-4 rounded-md border bg-muted/30 p-3">
                    {k === "interval" && (
                      <>
                        <Field
                          id="schedule-every"
                          label={t("schedule.every")}
                          error={errors["schedule.every"]}
                        >
                          {(aria) => (
                            <Select
                              value={String(value.every)}
                              onValueChange={(v) => set({ every: Number(v) })}
                            >
                              <SelectTrigger {...aria} className="w-24">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {STEPS[value.unit].map((n) => (
                                  <SelectItem key={n} value={String(n)}>
                                    {n}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          )}
                        </Field>
                        <Field
                          id="schedule-unit"
                          label={t("schedule.unit")}
                          error={errors["schedule.unit"]}
                        >
                          {(aria) => (
                            <Select
                              value={value.unit}
                              onValueChange={(v) => {
                                const unit = v as Unit;
                                set({
                                  unit,
                                  every: STEPS[unit].includes(value.every)
                                    ? value.every
                                    : STEPS[unit][0]!,
                                });
                              }}
                            >
                              <SelectTrigger {...aria} className="w-32">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {(["minutes", "hours"] as const).map((u) => (
                                  <SelectItem key={u} value={u}>
                                    {t(`schedule.units.${u}`)}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          )}
                        </Field>
                      </>
                    )}
                    {k === "weekly" && (
                      <fieldset
                        className="space-y-2"
                        aria-describedby={
                          errors["schedule.days"] ? "schedule-days-error" : undefined
                        }
                      >
                        <legend className="text-sm font-medium">{t("schedule.days")}</legend>
                        <div className="flex flex-wrap gap-1">
                          {WEEKDAYS.map((w, i) => {
                            const on = value.days.includes(w);
                            return (
                              <button
                                key={w}
                                type="button"
                                aria-pressed={on}
                                onClick={() =>
                                  set({
                                    days: on
                                      ? value.days.filter((x) => x !== w)
                                      : [...value.days, w],
                                  })
                                }
                                className={cn(
                                  "h-9 min-w-11 rounded-md border px-2 text-sm capitalize focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                                  on
                                    ? "border-primary bg-primary text-primary-foreground"
                                    : "bg-background hover:bg-accent",
                                )}
                              >
                                {/* The name comes from the full day name; the
                                    abbreviation is only visual. */}
                                <span aria-hidden="true">{shortDays[i]}</span>
                                <span className="sr-only">{longDays[i]}</span>
                              </button>
                            );
                          })}
                        </div>
                        {errors["schedule.days"] && (
                          <p id="schedule-days-error" className="text-sm text-destructive">
                            {errors["schedule.days"]}
                          </p>
                        )}
                      </fieldset>
                    )}
                    {k === "monthly" && (
                      <Field
                        id="schedule-day"
                        label={t("schedule.day")}
                        hint={value.day > 28 ? t("schedule.dayHint") : undefined}
                        error={errors["schedule.day"]}
                      >
                        {(aria) => (
                          <Select
                            value={String(value.day)}
                            onValueChange={(v) => set({ day: Number(v) })}
                          >
                            <SelectTrigger {...aria} className="w-24">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              {Array.from({ length: 31 }, (_, i) => i + 1).map((n) => (
                                <SelectItem key={n} value={String(n)}>
                                  {n}
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                      </Field>
                    )}
                    {k !== "interval" && timeField}
                  </div>
                )}
              </div>
            ))}
          </RadioGroup>
        </TabsContent>

        <TabsContent value="advanced" className="pt-3">
          <Field
            id="schedule-expr"
            label={t("schedule.expr")}
            hint={t("schedule.exprHint")}
            error={errors["schedule.expr"]}
          >
            {(aria) => (
              <Input
                {...aria}
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
                placeholder="0 7 * * 1-5"
                value={value.expr}
                onChange={(e) => set({ expr: e.target.value })}
              />
            )}
          </Field>
        </TabsContent>
      </Tabs>

      {scheduleError && <p className="text-sm text-destructive">{scheduleError}</p>}

      <Field
        id="schedule-timezone"
        label={t("schedule.timezone")}
        hint={t("schedule.timezoneHint", { tz: defaultTimezone })}
        error={errors.timezone}
      >
        {(aria) => (
          <>
            <Input
              {...aria}
              list="schedule-timezones"
              spellCheck={false}
              autoComplete="off"
              className="max-w-xs"
              value={timezone}
              onChange={(e) => onTimezoneChange(e.target.value)}
            />
            <datalist id="schedule-timezones">
              {zones.map((z) => (
                <option key={z} value={z} />
              ))}
            </datalist>
          </>
        )}
      </Field>

      {!manual && (
        <div className="flex items-start gap-3">
          <Switch
            id="schedule-builtin"
            checked={builtIn}
            onCheckedChange={onBuiltInChange}
            aria-describedby="schedule-builtin-hint"
            className="mt-0.5"
          />
          <div className="space-y-1">
            <Label htmlFor="schedule-builtin">{t("schedule.builtIn")}</Label>
            <p id="schedule-builtin-hint" className="text-sm text-muted-foreground">
              {t("schedule.builtInHint")}
            </p>
          </div>
        </div>
      )}

      <section
        aria-labelledby="schedule-preview-title"
        aria-live="polite"
        aria-busy={previewPending}
        className="space-y-3 rounded-lg border p-4"
      >
        <h3 id="schedule-preview-title" className="text-sm font-medium text-muted-foreground">
          {t("schedule.preview")}
        </h3>
        {preview ? (
          <>
            <p className="font-medium" data-testid="schedule-sentence">
              {preview.description}
            </p>
            {preview.cron ? (
              <>
                <p className="text-sm">
                  <span className="text-muted-foreground">{t("schedule.cron")}: </span>
                  <code
                    className="rounded bg-muted px-1.5 py-0.5 font-mono"
                    data-testid="schedule-cron"
                  >
                    {preview.cron}
                  </code>
                </p>
                {!!preview.next.length && (
                  <div className="text-sm">
                    <p className="text-muted-foreground">{t("schedule.nextRuns")}</p>
                    <ol className="mt-1 list-inside list-decimal space-y-0.5">
                      {preview.next.map((n) => (
                        <li key={n}>
                          <time dateTime={n}>{nextRun(n)}</time>
                        </li>
                      ))}
                    </ol>
                  </div>
                )}
                {!builtIn && (
                  <p className="text-sm text-muted-foreground">{t("schedule.builtInOff")}</p>
                )}
              </>
            ) : (
              <p className="text-sm text-muted-foreground">{t("schedule.manualPreview")}</p>
            )}
          </>
        ) : (
          <p className="text-sm text-muted-foreground">
            {previewPending ? t("schedule.loading") : "—"}
          </p>
        )}
      </section>
    </div>
  );
}
