import { vi } from "vitest";

import type {
  Connection,
  ConnectorKind,
  Counts,
  Flow,
  Run,
  RunDetail,
  RunEvent,
  Session,
} from "@/api/client";

/**
 * A fake backend for component tests. Routes are "METHOD /path" with
 * ":param" segments, relative to /api/v1 (or absolute, like /healthz).
 * A handler returns a Response, or any value to send as JSON with 200.
 */
export type Handler = (ctx: {
  params: Record<string, string>;
  url: URL;
  body: unknown;
  request: Request;
}) => unknown;

export type Call = { method: string; path: string; body: unknown };

export const desktopSession: Session = {
  authenticated: true,
  mode: "desktop",
  mustChangePassword: false,
  version: "1.2.3",
  timezone: "Europe/Rome",
};

export const webSession = (over: Partial<Session> = {}): Session => ({
  authenticated: true,
  mode: "web",
  mustChangePassword: false,
  csrfToken: "csrf-1",
  version: "1.2.3",
  timezone: "Europe/Rome",
  ...over,
});

export const obsidianKind: ConnectorKind = {
  kind: "obsidian",
  displayName: "Obsidian",
  canRead: true,
  canWrite: true,
  fields: [{ name: "path", type: "path", required: true }],
  locationFields: [{ name: "folder", type: "string", required: false }],
};

export const zeroCounts: Counts = {
  created: 0,
  updated: 0,
  moved: 0,
  deleted: 0,
  archived: 0,
  kept: 0,
  unchanged: 0,
  failed: 0,
  attachmentsUploaded: 0,
  attachmentsDeleted: 0,
};

const stamp = "2026-11-20T10:00:00Z";

export const makeConnection = (over: Partial<Connection> = {}): Connection => ({
  id: "c1",
  name: "Team vault",
  kind: "obsidian",
  config: { path: "/vaults/team" },
  hasSecret: false,
  usedBy: [],
  createdAt: stamp,
  updatedAt: stamp,
  ...over,
});

export const makeRun = (over: Partial<Run> = {}): Run => ({
  id: "r1",
  flowId: "f1",
  flowName: "Handbook",
  trigger: "manual",
  dryRun: false,
  status: "succeeded",
  startedAt: stamp,
  finishedAt: "2026-11-20T10:01:05Z",
  counts: { ...zeroCounts, created: 3 },
  ...over,
});

export const makeRunDetail = (over: Partial<RunDetail> = {}): RunDetail => ({
  ...makeRun(),
  report: { dryRun: false, pages: 3, counts: { ...zeroCounts, created: 3 }, targets: [] },
  ...over,
});

export const makeFlow = (over: Partial<Flow> = {}): Flow => ({
  id: "f1",
  name: "Handbook",
  enabled: true,
  sourceConnectionId: "c1",
  scope: { mode: "all" },
  options: { deletePolicy: "archive", attachments: true, banner: true },
  schedule: { kind: "manual" },
  scheduleEnabled: false,
  timezone: "Europe/Rome",
  scheduleText: "Only when run manually",
  targets: [{ id: "t1", connectionId: "c2", location: {} }],
  createdAt: stamp,
  updatedAt: stamp,
  ...over,
});

export const makeEvent = (id: number, msg: string, over: Partial<RunEvent> = {}): RunEvent => ({
  id,
  runId: "r1",
  time: stamp,
  level: "INFO",
  msg,
  attrs: {},
  ...over,
});

const defaults: Record<string, Handler> = {
  "GET /healthz": () => ({ status: "ok", database: "ok", version: "1.2.3" }),
  "GET /auth/session": () => desktopSession,
  "GET /connector-kinds": () => [obsidianKind],
  "GET /connections": () => [],
  "GET /flows": () => [],
  "GET /runs": () => ({ items: [] }),
  "POST /schedules/preview": ({ body }) => {
    const { schedule, timezone = "Europe/Rome" } = body as {
      schedule: { kind: string };
      timezone?: string;
    };
    return schedule.kind === "manual"
      ? { description: "Only when run manually", cron: "", timezone, next: [] }
      : {
          description: `A ${schedule.kind} schedule, ${timezone}`,
          cron: "0 7 * * 1-5",
          timezone,
          next: ["2026-11-27T06:00:00Z", "2026-11-30T06:00:00Z"],
        };
  },
};

export function apiError(status: number, code: string, message = code, fields?: object) {
  return Response.json({ error: { code, message, fields } }, { status });
}

function match(pattern: string, path: string): Record<string, string> | null {
  const p = pattern.split("/");
  const a = path.split("/");
  if (p.length !== a.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < p.length; i++) {
    if (p[i]!.startsWith(":")) params[p[i]!.slice(1)] = decodeURIComponent(a[i]!);
    else if (p[i] !== a[i]) return null;
  }
  return params;
}

/** Stub fetch with the default routes plus `routes`; returns the calls made. */
export function mockApi(routes: Record<string, Handler> = {}) {
  const all = { ...defaults, ...routes };
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request =
      input instanceof Request ? input : new Request(new URL(String(input), location.href), init);
    const url = new URL(request.url);
    const path = url.pathname.replace(/^\/api\/v1(?=\/)/, "");
    const text = await request.text();
    const body: unknown = text ? JSON.parse(text) : undefined;
    calls.push({ method: request.method, path, body });
    for (const [key, handler] of Object.entries(all)) {
      const [method, pattern] = key.split(" ") as [string, string];
      if (method !== request.method) continue;
      const params = match(pattern, path);
      if (!params) continue;
      const out = await handler({ params, url, body, request });
      return out instanceof Response ? out : Response.json(out ?? null);
    }
    return apiError(404, "not_found", `no mock for ${request.method} ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetch: fetchMock };
}

type Listener = (e: MessageEvent<string>) => void;

/** A controllable EventSource; the latest instance is FakeEventSource.last. */
export class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];
  static get last() {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }

  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, Listener[]>();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }

  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.();
  }

  emit(type: string, data: unknown) {
    const e = new MessageEvent(type, { data: JSON.stringify(data) });
    for (const fn of this.listeners.get(type) ?? []) fn(e);
  }

  fail(closed = false) {
    if (closed) this.readyState = FakeEventSource.CLOSED;
    this.onerror?.();
  }
}

export function stubEventSource() {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
}
