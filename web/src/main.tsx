import "./index.css";
import "./i18n";

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";

import { applyTheme, readStoredTheme } from "@/lib/theme";
import { AppProviders } from "@/providers";
import { routes } from "@/routes";

// Apply the stored theme before the first paint. (No inline script in
// index.html: the server's CSP forbids inline scripts.)
applyTheme(readStoredTheme());

const router = createBrowserRouter(routes);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
);
