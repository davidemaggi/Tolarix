import { useTranslation } from "react-i18next";

// Locale-aware formatters bound to the active UI language.
export function useFormatters() {
  const { i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  return {
    locale,
    dateTime: (value: Date | string | number, opts?: Intl.DateTimeFormatOptions) =>
      new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short", ...opts }).format(
        new Date(value),
      ),
    number: (value: number, opts?: Intl.NumberFormatOptions) =>
      new Intl.NumberFormat(locale, opts).format(value),
    relativeTime: (value: number, unit: Intl.RelativeTimeFormatUnit) =>
      new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(value, unit),
  };
}

/** Duration between two timestamps, e.g. "1 min 5 s". */
export function useDuration() {
  const fmt = useFormatters();
  return (start: string, end?: string) => {
    if (!end) return "";
    const s = Math.max(0, Math.round((Date.parse(end) - Date.parse(start)) / 1000));
    const unit = (value: number, u: "hour" | "minute" | "second") =>
      fmt.number(value, { style: "unit", unit: u, unitDisplay: "narrow" });
    if (s < 60) return unit(s, "second");
    if (s < 3600) return `${unit(Math.floor(s / 60), "minute")} ${unit(s % 60, "second")}`;
    return `${unit(Math.floor(s / 3600), "hour")} ${unit(Math.floor((s % 3600) / 60), "minute")}`;
  };
}
