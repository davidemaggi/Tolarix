import type { RouteObject } from "react-router";

import { AppLayout } from "@/components/app-layout";
import { SessionGate } from "@/components/session-gate";
import { DashboardPage } from "@/pages/dashboard";
import { NotFoundPage } from "@/pages/not-found";

// The dashboard is the landing page and ships in the main bundle; the other
// pages load on first visit.
export const routes: RouteObject[] = [
  {
    element: (
      <SessionGate>
        <AppLayout />
      </SessionGate>
    ),
    children: [
      { index: true, element: <DashboardPage /> },
      {
        path: "connections",
        lazy: async () => ({ Component: (await import("@/pages/connections")).ConnectionsPage }),
      },
      {
        path: "connections/new",
        lazy: async () => ({
          Component: (await import("@/pages/connection-form")).ConnectionFormPage,
        }),
      },
      {
        path: "connections/:id",
        lazy: async () => ({
          Component: (await import("@/pages/connection-form")).ConnectionFormPage,
        }),
      },
      {
        path: "flows",
        lazy: async () => ({ Component: (await import("@/pages/flows")).FlowsPage }),
      },
      {
        path: "flows/new",
        lazy: async () => ({ Component: (await import("@/pages/flow-wizard")).FlowWizardPage }),
      },
      {
        path: "flows/:id",
        lazy: async () => ({ Component: (await import("@/pages/flow-detail")).FlowDetailPage }),
      },
      {
        path: "flows/:id/edit",
        lazy: async () => ({ Component: (await import("@/pages/flow-wizard")).FlowWizardPage }),
      },
      {
        path: "runs",
        lazy: async () => ({ Component: (await import("@/pages/runs")).RunsPage }),
      },
      {
        path: "runs/:id",
        lazy: async () => ({ Component: (await import("@/pages/run-detail")).RunDetailPage }),
      },
      {
        path: "settings",
        lazy: async () => ({ Component: (await import("@/pages/settings")).SettingsPage }),
      },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
];
