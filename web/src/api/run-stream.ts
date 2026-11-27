import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { API_BASE, type Run, type RunEvent } from "./client";
import { keys } from "./queries";

export type StreamState = "connecting" | "live" | "ended" | "error";

/** Keep at most this many lines in memory; the full log stays on the server. */
export const MAX_LINES = 5000;

/**
 * Follow a run's log over Server-Sent Events. The server replays stored
 * lines first, so this also shows the log of finished runs. EventSource
 * reconnects by itself and resumes with Last-Event-ID.
 *
 * State is not reset when runId changes: mount one consumer per run
 * (for example with key={runId}).
 */
export function useRunStream(runId: string | undefined) {
  const qc = useQueryClient();
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [state, setState] = useState<StreamState>("connecting");
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    if (!runId) return;
    const es = new EventSource(`${API_BASE}/runs/${encodeURIComponent(runId)}/stream`);
    let pending: RunEvent[] = [];
    let frame = 0;
    // Batch lines per animation frame: a replay can deliver thousands at once.
    const flush = () => {
      frame = 0;
      const batch = pending;
      pending = [];
      setEvents((prev) => {
        const next = prev.concat(batch);
        if (next.length > MAX_LINES) {
          setTruncated(true);
          return next.slice(next.length - MAX_LINES);
        }
        return next;
      });
    };
    es.onopen = () => setState("live");
    es.addEventListener("log", (e) => {
      pending.push(JSON.parse((e as MessageEvent<string>).data) as RunEvent);
      if (!frame) frame = requestAnimationFrame(flush);
    });
    es.addEventListener("end", (e) => {
      es.close();
      if (frame) cancelAnimationFrame(frame);
      flush();
      const run = JSON.parse((e as MessageEvent<string>).data) as Run;
      setState("ended");
      void qc.invalidateQueries({ queryKey: keys.run(run.id) });
      void qc.invalidateQueries({ queryKey: keys.flows });
      void qc.invalidateQueries({ queryKey: keys.runs });
    });
    es.onerror = () => {
      // CLOSED means the browser gave up (for example a 404); otherwise it retries.
      if (es.readyState === EventSource.CLOSED) setState("error");
      else setState("connecting");
    };
    return () => {
      es.close();
      if (frame) cancelAnimationFrame(frame);
    };
  }, [runId, qc]);

  return { events, state, truncated };
}
