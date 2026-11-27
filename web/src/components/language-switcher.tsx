import { Languages } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SUPPORTED_LANGUAGES } from "@/i18n";

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const current = i18n.resolvedLanguage ?? "en";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        {/* The visible code must be part of the accessible name (WCAG 2.5.3). */}
        <Button variant="ghost" size="sm">
          <Languages aria-hidden="true" />
          <span className="sr-only">{t("language.toggle")}: </span>
          <span className="uppercase">{current}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>{t("language.label")}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={current}
          onValueChange={(lng) => void i18n.changeLanguage(lng)}
        >
          {SUPPORTED_LANGUAGES.map((lng) => (
            <DropdownMenuRadioItem key={lng} value={lng} lang={lng}>
              {t(`language.${lng}`)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
