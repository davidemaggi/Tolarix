import type { TFunction } from "i18next";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";

import type { Schedule } from "@/api/client";
import { ScheduleEditor } from "@/components/schedule-editor";
import { scheduleDraft, scheduleOf, validateSchedule, type ScheduleDraft } from "@/lib/schedule";

const t = ((key: string) => key) as unknown as TFunction;

describe("schedule draft", () => {
  it("round-trips every kind and keeps only its fields", () => {
    for (const s of [
      { kind: "manual" },
      { kind: "interval", every: 2, unit: "hours" },
      { kind: "daily", time: "06:45" },
      { kind: "weekly", days: ["mon", "fri"], time: "07:30" },
      { kind: "monthly", day: 31, time: "06:00" },
      { kind: "cron", expr: "0 7 * * 1-5" },
    ] satisfies Schedule[]) {
      expect(scheduleOf(scheduleDraft(s))).toEqual(s);
    }
    // Switching kinds keeps what was typed for the others.
    const d = {
      ...scheduleDraft({ kind: "monthly", day: 15, time: "09:00" }),
      kind: "daily" as const,
    };
    expect(scheduleOf(d)).toEqual({ kind: "daily", time: "09:00" });
    expect(scheduleOf({ ...d, kind: "monthly" })).toEqual({
      kind: "monthly",
      day: 15,
      time: "09:00",
    });
  });

  it("sorts weekdays and trims the expression", () => {
    const d = scheduleDraft({ kind: "weekly", days: ["sun", "mon", "wed"], time: "07:30" });
    expect(scheduleOf(d).days).toEqual(["mon", "wed", "sun"]);
    expect(scheduleOf({ ...d, tab: "advanced", expr: "  @daily " })).toEqual({
      kind: "cron",
      expr: "@daily",
    });
  });

  it("validates what the server would reject outright", () => {
    const weekly = scheduleDraft({ kind: "weekly", days: ["mon"], time: "07:30" });
    expect(validateSchedule(weekly, "Europe/Rome", t)).toEqual({});
    expect(validateSchedule({ ...weekly, days: [] }, " ", t)).toEqual({
      "schedule.days": "schedule.daysRequired",
      timezone: "schedule.timezoneRequired",
    });
    expect(validateSchedule({ ...weekly, tab: "advanced", expr: "" }, "UTC", t)).toEqual({
      "schedule.expr": "schedule.exprRequired",
    });
  });
});

function Harness({ onChange }: { onChange: (d: ScheduleDraft) => void }) {
  const [d, setD] = useState(scheduleDraft({ kind: "interval", every: 20, unit: "minutes" }));
  return (
    <ScheduleEditor
      value={d}
      onChange={(next) => {
        setD(next);
        onChange(next);
      }}
      timezone="UTC"
      defaultTimezone="UTC"
      onTimezoneChange={() => {}}
      builtIn
      onBuiltInChange={() => {}}
      previewPending={false}
      errors={{}}
    />
  );
}

describe("schedule editor", () => {
  it("resets the interval to a valid step when the unit changes", async () => {
    const user = userEvent.setup();
    let last: ScheduleDraft | undefined;
    render(<Harness onChange={(d) => (last = d)} />);

    await user.click(screen.getByRole("combobox", { name: "Unit" }));
    await user.click(await screen.findByRole("option", { name: "hours" }));
    expect(last && scheduleOf(last)).toEqual({ kind: "interval", every: 1, unit: "hours" });

    await user.click(screen.getByRole("combobox", { name: "Every" }));
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "1",
      "2",
      "3",
      "4",
      "6",
      "8",
      "12",
    ]);
  });
});
