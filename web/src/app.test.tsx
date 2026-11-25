import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";

import { LANGUAGE_STORAGE_KEY } from "@/i18n";
import { THEME_STORAGE_KEY } from "@/lib/theme";
import { mockApi } from "@/test/api";
import { renderApp } from "@/test/render";

beforeEach(() => {
  mockApi();
});
afterEach(() => vi.unstubAllGlobals());

describe("app shell", () => {
  it("renders the dashboard and the server status", async () => {
    renderApp();
    expect(await screen.findByRole("heading", { level: 1, name: "Dashboard" })).toBeInTheDocument();
    expect(await screen.findByText(/Running, version 1\.2\.3/)).toBeInTheDocument();
  });

  it("navigates with the main navigation", async () => {
    const user = userEvent.setup();
    renderApp();
    const nav = await screen.findByRole("navigation", { name: "Main navigation" });
    await user.click(within(nav).getAllByRole("link", { name: "Flows" })[0]!);
    expect(await screen.findByRole("heading", { level: 1, name: "Flows" })).toBeInTheDocument();
  });

  it("shows a not-found page for unknown routes", async () => {
    renderApp("/nope");
    expect(await screen.findByRole("heading", { name: "Page not found" })).toBeInTheDocument();
  });

  it("reports an unreachable server", async () => {
    mockApi({ "GET /healthz": () => new Response("", { status: 503 }) });
    renderApp();
    expect(await screen.findByText(/Not reachable/, {}, { timeout: 3000 })).toBeInTheDocument();
  });
});

describe("language switcher", () => {
  it("switches to Italian, persists the choice and sets <html lang>", async () => {
    const user = userEvent.setup();
    renderApp();
    await user.click(await screen.findByRole("button", { name: "Change language" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Italiano" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Panoramica" }),
    ).toBeInTheDocument();
    expect(window.localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe("it");
    expect(document.documentElement.lang).toBe("it");
  });
});

describe("theme toggle", () => {
  it("applies dark and light, and persists the choice", async () => {
    const user = userEvent.setup();
    renderApp();

    await user.click(await screen.findByRole("button", { name: "Change theme" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Dark" }));
    await waitFor(() => expect(document.documentElement).toHaveClass("dark"));
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");

    await user.click(screen.getByRole("button", { name: "Change theme" }));
    await user.click(await screen.findByRole("menuitemradio", { name: "Light" }));
    await waitFor(() => expect(document.documentElement).not.toHaveClass("dark"));
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
  });

  it("is reachable by keyboard", async () => {
    const user = userEvent.setup();
    renderApp();
    const trigger = await screen.findByRole("button", { name: "Change theme" });
    trigger.focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("menu")).toBeInTheDocument();
  });
});
