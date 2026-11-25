import { useTranslation } from "react-i18next";

/** "baseUrl" → "Base url"; used when a field has no translation. */
export function humanize(name: string): string {
  const words = name
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .replace(/[_-]+/g, " ")
    .toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * Labels and hints for connector settings. Translations live under
 * connectorFields.<kind>.<field> (label) and .<field>Hint (hint); fields
 * without one fall back to a readable form of their name.
 */
export function useConnectorText() {
  const { t, i18n } = useTranslation();
  const lookup = (key: string) =>
    i18n.exists(key) ? (t as (k: string) => string)(key) : undefined;
  return {
    label: (kind: string, field: string) =>
      lookup(`connectorFields.${kind}.${field}`) ?? humanize(field),
    hint: (kind: string, field: string) => lookup(`connectorFields.${kind}.${field}Hint`),
    kindDescription: (kind: string) => lookup(`connectorKinds.${kind}`),
  };
}
