import { Monitor, Moon, Sun } from "lucide-react";
import { useTranslation } from "react-i18next";

import { useTheme } from "@/hooks/use-theme";
import type { Theme } from "@/lib/theme";
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

const icons = { light: Sun, dark: Moon, system: Monitor } satisfies Record<Theme, unknown>;

export function ThemeToggle() {
  const { t } = useTranslation();
  const { theme, setTheme } = useTheme();
  const Icon = icons[theme];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t("theme.toggle")}>
          <Icon aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>{t("theme.label")}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          {(["light", "dark", "system"] as const).map((value) => {
            const ItemIcon = icons[value];
            return (
              <DropdownMenuRadioItem key={value} value={value}>
                <ItemIcon aria-hidden="true" />
                {t(`theme.${value}`)}
              </DropdownMenuRadioItem>
            );
          })}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
