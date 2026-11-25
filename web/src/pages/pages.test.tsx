import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";

import type { ConnectorKind } from "@/api/client";
import {
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
    });
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
