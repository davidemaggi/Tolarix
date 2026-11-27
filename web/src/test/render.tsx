import { render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";

import { AppProviders } from "@/providers";
import { routes } from "@/routes";

export function renderApp(path = "/") {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  return render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  );
}
