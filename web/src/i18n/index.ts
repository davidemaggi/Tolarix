import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";

import en from "./locales/en.json";
import it from "./locales/it.json";

export const SUPPORTED_LANGUAGES = ["en", "it"] as const;
export type Language = (typeof SUPPORTED_LANGUAGES)[number];
export const LANGUAGE_STORAGE_KEY = "tolarix-lang";

export const resources = {
  en: { translation: en },
  it: { translation: it },
} as const;

// Resources are bundled, so init completes synchronously and no Suspense is needed.
void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: "en",
    supportedLngs: SUPPORTED_LANGUAGES,
    nonExplicitSupportedLngs: true,
    load: "languageOnly",
    interpolation: { escapeValue: false }, // React escapes already
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: LANGUAGE_STORAGE_KEY,
      caches: ["localStorage"],
    },
    returnNull: false,
  });

const syncHtmlLang = (lng: string) => {
  if (typeof document !== "undefined") document.documentElement.lang = lng;
};
syncHtmlLang(i18n.resolvedLanguage ?? "en");
i18n.on("languageChanged", syncHtmlLang);

export default i18n;
