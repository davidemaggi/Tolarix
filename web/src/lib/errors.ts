import type { TFunction } from "i18next";

import { ApiError } from "@/api/client";

// API error codes with a translated message; others show the server's text.
const known = {
  network: "errors.network",
  invalid_password: "errors.invalidPassword",
  too_many_attempts: "errors.tooManyAttempts",
  csrf: "errors.csrf",
  running: "errors.running",
  not_here: "errors.notHere",
  not_running: "errors.notRunning",
  name_taken: "errors.nameTaken",
  in_use: "errors.inUse",
} as const;

/** A plain-language message for any error thrown by the API layer. */
export function errorMessage(err: unknown, t: TFunction): string {
  if (err instanceof ApiError) {
    // These carry details (flow names, start time) only the server knows.
    if ((err.code === "in_use" || err.code === "running") && err.message) return err.message;
    const key = known[err.code as keyof typeof known];
    if (key) return t(key);
    return err.message || t("errors.generic");
  }
  return t("errors.generic");
}

/** Per-field validation messages of a 400 validation answer. */
export function fieldErrors(err: unknown): Record<string, string> {
  return err instanceof ApiError ? err.fields : {};
}
