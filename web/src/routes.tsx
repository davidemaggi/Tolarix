import type { RouteObject } from "react-router";

import { AppLayout } from "@/components/app-layout";
import { DashboardPage } from "@/pages/dashboard";
import { NotFoundPage } from "@/pages/not-found";
import { PlaceholderPage } from "@/pages/placeholder";

export const routes: RouteObject[] = [
  {
    element: <AppLayout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "connections", element: <PlaceholderPage section="connections" /> },
      { path: "flows", element: <PlaceholderPage section="flows" /> },
      { path: "settings", element: <PlaceholderPage section="settings" /> },
      { path: "*", element: <NotFoundPage /> },
    ],
  },
];
