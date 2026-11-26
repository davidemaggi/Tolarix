import type { TFunction } from "i18next";

import type { Schedule } from "@/api/client";

export type Weekday = NonNullable<Schedule["days"]>[number];
export type Unit = NonNullable<Schedule["unit"]>;
export type SimpleKind = Exclude<Schedule["kind"], "cron">;

export const WEEKDAYS: Weekday[] = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
// The interval steps the server accepts (they divide the hour or the day).
export const STEPS: Record<Unit, number[]> = {
  minutes: [5, 10, 15, 20, 30],
  hours: [1, 2, 3, 4, 6, 8, 12],
};

/**
 * The editor's state. It keeps the fields of every kind so switching kinds
 * back and forth does not lose what was typed.
 */
export type ScheduleDraft = {
  tab: "simple" | "advanced";
  kind: SimpleKind;
  every: number;
  unit: Unit;
  time: string;
  days: Weekday[];
  day: number;
  expr: string;
};

export function scheduleDraft(s?: Schedule): ScheduleDraft {
  const d: ScheduleDraft = {
    tab: "simple",
    kind: "manual",
    every: 30,
    unit: "minutes",
    time: "07:30",
    days: ["mon", "tue", "wed", "thu", "fri"],
    day: 1,
    expr: "",
  };
  if (!s) return d;
  if (s.kind === "cron") return { ...d, tab: "advanced", expr: s.expr ?? "" };
  return {
    ...d,
    kind: s.kind,
    every: s.every ?? d.every,
    unit: s.unit ?? d.unit,
    time: s.time ?? d.time,
    days: s.days?.length ? s.days : d.days,
    day: s.day ?? d.day,
  };
}

/** The schedule the draft describes, with only the fields of its kind. */
export function scheduleOf(d: ScheduleDraft): Schedule {
  if (d.tab === "advanced") return { kind: "cron", expr: d.expr.trim() };
  switch (d.kind) {
    case "interval":
      return { kind: "interval", every: d.every, unit: d.unit };
    case "daily":
      return { kind: "daily", time: d.time };
    case "weekly":
      return { kind: "weekly", days: WEEKDAYS.filter((w) => d.days.includes(w)), time: d.time };
    case "monthly":
      return { kind: "monthly", day: d.day, time: d.time };
    default:
      return { kind: "manual" };
  }
}

/** Client-side checks; the server checks the rest (and the preview shows it). */
export function validateSchedule(
  d: ScheduleDraft,
  timezone: string,
  t: TFunction,
): Record<string, string> {
  const errs: Record<string, string> = {};
  const s = scheduleOf(d);
  if (s.kind === "cron" && !s.expr) errs["schedule.expr"] = t("schedule.exprRequired");
  if (s.kind === "weekly" && !s.days?.length) errs["schedule.days"] = t("schedule.daysRequired");
  if (["daily", "weekly", "monthly"].includes(s.kind) && !s.time) {
    errs["schedule.time"] = t("validation.required");
  }
  if (!timezone.trim()) errs.timezone = t("schedule.timezoneRequired");
  return errs;
}
