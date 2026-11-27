import createClient, { type Middleware } from "openapi-fetch";

import i18n from "@/i18n";

import type { components, paths } from "./schema.gen";

export type Schemas = components["schemas"];
export type Session = Schemas["Session"];
export type ConnectorKind = Schemas["ConnectorKind"];
export type ConnectorField = Schemas["ConnectorField"];
export type Connection = Schemas["Connection"];
export type ConnectionInput = Schemas["ConnectionInput"];
export type BrowseNode = Schemas["BrowseNode"];
export type Flow = Schemas["Flow"];
export type FlowInput = Schemas["FlowInput"];
export type FlowOptions = Schemas["FlowOptions"];
export type Scope = Schemas["Scope"];
export type Schedule = Schemas["Schedule"];
export type SchedulePreview = Schemas["SchedulePreview"];
export type Run = Schemas["Run"];
export type RunDetail = Schemas["RunDetail"];
export type RunStatus = Schemas["RunStatus"];
export type RunEvent = Schemas["RunEvent"];
export type Counts = Schemas["Counts"];
export type Config = Record<string, unknown>;

export const API_BASE = "/api/v1";
const CSRF_HEADER = "X-CSRF-Token";

/** An error answer of the API (or a network failure, with status 0). */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields: Record<string, string>;

  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields ?? {};
  }
}

let csrfToken = "";

/** Remember the session's CSRF token; it is sent on every POST/PUT/DELETE. */
export function setCsrfToken(token: string | undefined) {
  csrfToken = token ?? "";
}

type AuthListener = (reason: "signed-out" | "password-change") => void;
let authListener: AuthListener | undefined;

/** Called when the server says the session ended or the password must change. */
export function onAuthProblem(listener: AuthListener | undefined) {
  authListener = listener;
}

const middleware: Middleware = {
  onRequest({ request }) {
    // Sentences the server writes (schedules) follow the UI language.
    request.headers.set("Accept-Language", i18n.resolvedLanguage ?? "en");
    if (!["GET", "HEAD", "OPTIONS"].includes(request.method) && csrfToken) {
      request.headers.set(CSRF_HEADER, csrfToken);
    }
    return request;
  },
  async onResponse({ request, response }) {
    if (response.ok || !authListener) return response;
    const login = new URL(request.url, window.location.origin).pathname.endsWith("/auth/login");
    if (response.status === 401 && !login) {
      authListener("signed-out");
    } else if (response.status === 403) {
      const body = (await response
        .clone()
        .json()
        .catch(() => null)) as {
        error?: { code?: string };
      } | null;
      if (body?.error?.code === "password_change_required") authListener("password-change");
    }
    return response;
  },
};

export const api = createClient<paths>({
  // Absolute, because openapi-fetch builds Request objects and Node (tests)
  // rejects relative URLs; in the browser this is the same origin.
  baseUrl: new URL(API_BASE, window.location.origin).href.replace(/\/$/, ""),
  credentials: "same-origin",
  // Resolved at call time so tests can stub the global fetch.
  fetch: (req) => globalThis.fetch(req),
});
api.use(middleware);

type Result<T> = { data?: T; error?: unknown; response: Response };

/** Return the data of an openapi-fetch call, or throw an ApiError. */
export async function unwrap<T>(call: Promise<Result<T>>): Promise<T> {
  let res: Result<T>;
  try {
    res = await call;
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, "network", e instanceof Error ? e.message : String(e));
  }
  if (res.response.ok) return res.data as T;
  const body = res.error as {
    error?: { code?: string; message?: string; fields?: Record<string, string> };
  };
  const err = body?.error;
  throw new ApiError(
    res.response.status,
    err?.code ?? "http_" + res.response.status,
    err?.message ?? res.response.statusText,
    err?.fields,
  );
}
