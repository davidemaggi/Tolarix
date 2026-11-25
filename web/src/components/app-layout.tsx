import { Cable, LayoutDashboard, Settings, Workflow } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NavLink, Outlet } from "react-router";

import { LanguageSwitcher } from "@/components/language-switcher";
import { ServerStatus } from "@/components/server-status";
import { ThemeToggle } from "@/components/theme-toggle";
import { cn } from "@/lib/utils";

const navItems = [
  { to: "/", key: "nav.dashboard", icon: LayoutDashboard, end: true },
  { to: "/connections", key: "nav.connections", icon: Cable, end: false },
  { to: "/flows", key: "nav.flows", icon: Workflow, end: false },
  { to: "/settings", key: "nav.settings", icon: Settings, end: false },
] as const;

export function AppLayout() {
  const { t } = useTranslation();
  return (
    <div className="flex min-h-svh flex-col">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-primary-foreground focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        {t("app.skipToContent")}
      </a>
      <header className="border-b">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-4 px-4">
          <NavLink to="/" className="flex items-center gap-2 font-semibold">
            <img src="/favicon.svg" alt="" className="size-6" />
            <span>{t("app.name")}</span>
          </NavLink>
          <nav
            aria-label={t("nav.label")}
            className="flex flex-1 items-center gap-1 overflow-x-auto"
          >
            {navItems.map(({ to, key, icon: Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    "flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium whitespace-nowrap text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none",
                    isActive && "bg-accent text-accent-foreground",
                  )
                }
              >
                <Icon className="size-4" aria-hidden="true" />
                <span className="sr-only sm:not-sr-only">{t(key)}</span>
              </NavLink>
            ))}
          </nav>
          <div className="flex items-center gap-1">
            <LanguageSwitcher />
            <ThemeToggle />
          </div>
        </div>
      </header>
      <main
        id="main"
        tabIndex={-1}
        className="mx-auto w-full max-w-6xl flex-1 px-4 py-8 outline-none"
      >
        <Outlet />
      </main>
      <footer className="border-t">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <ServerStatus />
          <span className="text-xs text-muted-foreground">{t("app.tagline")}</span>
        </div>
      </footer>
    </div>
  );
}
