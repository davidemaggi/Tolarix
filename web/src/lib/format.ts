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
