import { act, fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";

import type { ConnectorKind } from "@/api/client";
import {
  apiError,
  FakeEventSource,
  makeConnection,
  makeEvent,
  makeFlow,
  makeRun,
  makeRunDetail,
  mockApi,
  obsidianKind,
  stubEventSource,
} from "@/test/api";
import { renderApp } from "@/test/render";

beforeEach(() => stubEventSource());
afterEach(() => vi.unstubAllGlobals());

const master = makeConnection({ id: "c1", name: "Team vault" });
const mirror = makeConnection({ id: "c2", name: "Public vault", config: { path: "/vaults/pub" } });

describe("dashboard", () => {
  it("shows each flow with its wikis and last run, and starts a run", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      "GET /connections": () => [master, mirror],
      "GET /flows": () => [makeFlow({ lastRun: makeRun() })],
      "POST /flows/:id/runs": () => makeRun({ id: "r9", status: "running", finishedAt: undefined }),
      "GET /runs/:id": ({ params }) =>
        makeRunDetail({ id: params.id!, status: "running", finishedAt: undefined }),
    });
    renderApp();

    const list = await screen.findByRole("list", { name: "Flows" });
    const card = within(list).getByRole("article");
    expect(within(card).getByRole("link", { name: "Handbook" })).toHaveAttribute(
      "href",
      "/flows/f1",
    );
    expect(await within(card).findByText("Team vault")).toBeInTheDocument();
    expect(within(card).getByText("Public vault")).toBeInTheDocument();
    expect(within(card).getByText("Succeeded")).toBeInTheDocument();
    expect(within(card).getByText(/3 created/)).toBeInTheDocument();

    await user.click(within(card).getByRole("button", { name: "Dry-run Handbook" }));
    expect(calls.find((c) => c.method === "POST")).toMatchObject({
      path: "/flows/f1/runs",
      body: { dryRun: true },
    });
    // The run page opens on the live log.
    expect(await screen.findByRole("log", { name: "Log" })).toBeInTheDocument();
    expect(FakeEventSource.last?.url).toBe("/api/v1/runs/r9/stream");
  });

  it("guides a first-time user", async () => {
    mockApi();
    renderApp();
    expect(await screen.findByRole("heading", { name: "No flows yet" })).toBeInTheDocument();
  });
});

describe("connection form", () => {
  it("validates, then creates an Obsidian connection", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      "POST /connections": ({ body }) => makeConnection(body as object),
    });
    renderApp("/connections/new");

    await user.click(await screen.findByRole("button", { name: "Add connection" }));
    expect(screen.getByText("Choose the type of wiki.")).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: /Obsidian/ }));
    await user.click(screen.getByRole("button", { name: "Add connection" }));
    expect(screen.getAllByText("This field is required.")).toHaveLength(2);
    expect(screen.getByLabelText("Name")).toHaveAttribute("aria-invalid", "true");

    await user.type(screen.getByLabelText("Name"), "Team vault");
    await user.type(screen.getByLabelText("Vault folder"), " /vaults/team ");
    await user.click(screen.getByRole("button", { name: "Add connection" }));

    expect(await screen.findByRole("heading", { level: 1, name: "Connections" })).toBeVisible();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "Team vault",
      kind: "obsidian",
      config: { path: "/vaults/team" },
    });
  });

  it("keeps a stored secret unless it is replaced", async () => {
    const user = userEvent.setup();
    const wiki: ConnectorKind = {
      kind: "confluence",
      displayName: "Confluence",
      canRead: true,
      canWrite: true,
      fields: [
        { name: "url", type: "url", required: true },
        { name: "token", type: "secret", required: true },
      ],
      locationFields: [],
    };
    const existing = makeConnection({
      id: "c7",
      name: "Docs",
      kind: "confluence",
      config: { url: "https://wiki.example" },
      hasSecret: true,
    });
    const { calls } = mockApi({
      "GET /connector-kinds": () => [obsidianKind, wiki],
      "GET /connections/:id": () => existing,
      "PUT /connections/:id": ({ body }) => ({ ...existing, ...(body as object) }),
    });
    renderApp("/connections/c7");

    expect(await screen.findByText("A secret is stored. It is never shown.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByRole("heading", { level: 1, name: "Connections" });
    const first = calls.find((c) => c.method === "PUT")!.body as Record<string, unknown>;
    expect(first).not.toHaveProperty("secret");
    expect(first).toMatchObject({ name: "Docs", config: { url: "https://wiki.example" } });
  });
});

describe("flow wizard", () => {
  it("walks through the steps and creates the flow", async () => {
    const user = userEvent.setup();
    const { calls } = mockApi({
      "GET /connections": () => [master, mirror],
      "GET /connections/:id/browse": () => [
        { id: "p1", title: "Guides", hasChildren: false },
        { id: "p2", title: "Drafts", hasChildren: false },
      ],
      "POST /flows": ({ body }) => makeFlow({ ...(body as object), id: "f5" }),
      "GET /flows/:id": () => makeFlow({ id: "f5" }),
    });
    renderApp("/flows/new");

    const next = await screen.findByRole("button", { name: "Next" });
    await user.click(next);
    expect(screen.getByText("Choose the Master wiki.")).toBeInTheDocument();

    await user.type(screen.getByLabelText("Name"), "Handbook");
    await user.click(screen.getByRole("combobox", { name: "Master wiki" }));
    await user.click(await screen.findByRole("option", { name: "Team vault" }));
    await user.click(next);

    // Scope: leave out one page of the whole wiki.
    expect(await screen.findByRole("heading", { name: "What to mirror" })).toBeInTheDocument();
    await user.click(await screen.findByRole("checkbox", { name: "Drafts" }));
    await user.click(next);

    // Mirrors: the Master is not offered; pick the other vault.
    await screen.findByRole("heading", { name: "Mirrors" });
    await user.click(screen.getByRole("combobox", { name: "Wiki" }));
    expect(screen.queryByRole("option", { name: "Team vault" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("option", { name: "Public vault" }));
    await user.click(next);

    await screen.findByRole("heading", { name: "Options" });
    await user.click(next);
    await screen.findByRole("heading", { name: "Schedule" });
    await user.click(next);
    await screen.findByRole("heading", { name: "Review" });
    expect(screen.getByText("Left out: Drafts")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create flow" }));

    await screen.findByRole("heading", { level: 1, name: "Handbook" });
    expect(calls.find((c) => c.method === "POST" && c.path === "/flows")?.body).toMatchObject({
      name: "Handbook",
      sourceConnectionId: "c1",
      scope: { mode: "all", exclude: ["p2"] },
      targets: [{ connectionId: "c2", location: {} }],
      options: { deletePolicy: "archive", attachments: true, banner: true },
      schedule: { kind: "manual" },
      scheduleEnabled: true,
      timezone: "Europe/Rome",
    });
  });

  // Opens an existing flow in the wizard and walks to the schedule step.
  async function openSchedule(routes: Parameters<typeof mockApi>[0] = {}) {
    const user = userEvent.setup();
    const api = mockApi({
      "GET /connections": () => [master, mirror],
      "GET /connections/:id/browse": () => [],
      "GET /flows/:id": () => makeFlow({ id: "f5", scheduleEnabled: true }),
      "PUT /flows/:id": ({ body }) => makeFlow({ ...(body as object), id: "f5" }),
      ...routes,
    });
    renderApp("/flows/f5/edit");
    const next = await screen.findByRole("button", { name: "Next" });
    for (const step of ["What to mirror", "Mirrors", "Options", "Schedule"]) {
      await user.click(next);
      await screen.findByRole("heading", { name: step });
    }
    return { user, next, ...api };
  }

  it("sets a weekday schedule with a live preview", async () => {
    const { user, next, calls } = await openSchedule({
      "POST /schedules/preview": ({ body }) => {
        const { schedule } = body as { schedule: { kind: string; days?: string[] } };
        if (schedule.kind === "manual") {
          return {
            description: "Only when run manually",
            cron: "",
            timezone: "Europe/Rome",
            next: [],
          };
        }
        return {
          description: `Every ${schedule.days?.join("/")} at 08:15, Europe/Rome`,
          cron: "15 8 * * 1-4",
          timezone: "Europe/Rome",
          next: ["2026-11-30T07:15:00Z"],
        };
      },
    });
    expect(await screen.findByTestId("schedule-sentence")).toHaveTextContent(
      "Only when run manually",
    );
    // The built-in switch only matters for automatic schedules.
    expect(screen.queryByLabelText("Run with the built-in scheduler")).not.toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "On these weekdays at…" }));
    // Monday to Friday by default: leave Friday out and move the time.
    await user.click(screen.getByRole("button", { name: "Friday", pressed: true }));
    fireEvent.change(screen.getByLabelText("At"), { target: { value: "08:15" } });

    expect(
      await screen.findByText("Every mon/tue/wed/thu at 08:15, Europe/Rome"),
    ).toBeInTheDocument();
    expect(screen.getByTestId("schedule-cron")).toHaveTextContent("15 8 * * 1-4");
    const preview = screen.getByRole("region", { name: "Preview" });
    expect(within(preview).getAllByRole("listitem")).toHaveLength(1);
    expect(within(preview).getByRole("listitem")).toHaveTextContent("Monday");
    expect(calls.filter((c) => c.path === "/schedules/preview").at(-1)?.body).toMatchObject({
      schedule: { kind: "weekly", days: ["mon", "tue", "wed", "thu"], time: "08:15" },
      timezone: "Europe/Rome",
      lang: "en",
    });

    // Leave it to cron instead of the built-in scheduler.
    await user.click(screen.getByRole("switch", { name: "Run with the built-in scheduler" }));
    await user.click(next);
    await screen.findByRole("heading", { name: "Review" });
    expect(screen.getByText("Every mon/tue/wed/thu at 08:15, Europe/Rome")).toBeInTheDocument();
    expect(screen.getByText('Runs only via "tolarix run-due"')).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await screen.findByRole("heading", { level: 1, name: "Handbook" });
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({
      schedule: { kind: "weekly", days: ["mon", "tue", "wed", "thu"], time: "08:15" },
      scheduleEnabled: false,
      timezone: "Europe/Rome",
    });
  });

  it("checks a cron expression and shows the server's message", async () => {
    const { user, next } = await openSchedule({
      "POST /schedules/preview": ({ body }) => {
        const { schedule } = body as { schedule: { kind: string; expr?: string } };
        if (schedule.kind === "cron" && schedule.expr === "61 7 * * *") {
          return apiError(400, "invalid", "invalid", {
            "schedule.expr": "Minute 61 is out of range.",
          });
        }
        return {
          description: "Every day at 07:30, Europe/Rome",
          cron: schedule.kind === "cron" ? (schedule.expr ?? "") : "30 7 * * *",
          timezone: "Europe/Rome",
          next: [],
        };
      },
    });
    await user.click(screen.getByRole("radio", { name: "Every day at…" }));
    await screen.findByText("Every day at 07:30, Europe/Rome");

    // The advanced tab starts from what the simple form describes.
    await user.click(screen.getByRole("tab", { name: "Advanced (cron expression)" }));
    const expr = screen.getByLabelText("Cron expression");
    expect(expr).toHaveValue("30 7 * * *");

    await user.clear(expr);
    await user.click(next);
    expect(screen.getByText("Enter a cron expression.")).toBeInTheDocument();

    await user.type(expr, "61 7 * * *");
    expect(await screen.findByText("Minute 61 is out of range.")).toBeInTheDocument();
    expect(expr).toHaveAttribute("aria-invalid", "true");
  });
});

describe("run detail", () => {
  it("follows the live log and shows the report when the run ends", async () => {
    let finished = false;
    mockApi({
      "GET /runs/:id": () =>
        finished
          ? makeRunDetail({ id: "r1", counts: makeRun().counts })
          : makeRunDetail({ id: "r1", status: "running", finishedAt: undefined }),
    });
    renderApp("/runs/r1");

    const log = await screen.findByRole("log", { name: "Log" });
    const es = FakeEventSource.last!;
    act(() => es.open());
    expect(await screen.findByText("Live")).toBeInTheDocument();

    await act(async () => {
      es.emit("log", makeEvent(1, "reading the Master"));
      es.emit("log", makeEvent(2, "page failed", { level: "ERROR", attrs: { page: "Guides" } }));
      await new Promise((r) => requestAnimationFrame(r));
    });
    expect(within(log).getByText(/reading the Master/)).toBeInTheDocument();
    expect(within(log).getByText("page=Guides")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop run" })).toBeInTheDocument();

    finished = true;
    await act(async () => es.emit("end", makeRun({ id: "r1" })));
    expect(await screen.findByText("The run has ended.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Stop run" })).not.toBeInTheDocument();
    expect(es.readyState).toBe(FakeEventSource.CLOSED);
    expect(await screen.findByText("Succeeded")).toBeInTheDocument();
  });
});
