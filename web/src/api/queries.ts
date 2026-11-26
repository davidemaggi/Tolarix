import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useEffect, useMemo, useState } from "react";

import {
  api,
  setCsrfToken,
  unwrap,
  type BrowseNode,
  type ConnectionInput,
  type Config,
  type FlowInput,
  type RunStatus,
  type Schedule,
} from "./client";

export const keys = {
  session: ["session"] as const,
  kinds: ["connector-kinds"] as const,
  connections: ["connections"] as const,
  connection: (id: string) => ["connections", id] as const,
  browse: (id: string, parent: string) => ["connections", id, "browse", parent] as const,
  flows: ["flows"] as const,
  flow: (id: string) => ["flows", id] as const,
  runs: ["runs"] as const,
  runList: (f: RunFilters) => ["runs", "list", f] as const,
  run: (id: string) => ["runs", id] as const,
};

// ---- Session ----

export function useSession() {
  return useQuery({
    queryKey: keys.session,
    queryFn: async ({ signal }) => {
      const s = await unwrap(api.GET("/auth/session", { signal }));
      setCsrfToken(s.csrfToken);
      return s;
    },
    staleTime: 60_000,
    retry: 1,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (password: string) => unwrap(api.POST("/auth/login", { body: { password } })),
    onSuccess: (s) => {
      setCsrfToken(s.csrfToken);
      qc.setQueryData(keys.session, s);
    },
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => unwrap(api.POST("/auth/logout")),
    onSettled: async () => {
      setCsrfToken(undefined);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
      await qc.invalidateQueries({ queryKey: keys.session });
    },
  });
}

export function useChangePassword() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { currentPassword: string; newPassword: string }) =>
      unwrap(api.POST("/auth/password", { body })),
    onSuccess: (s) => {
      setCsrfToken(s.csrfToken);
      qc.setQueryData(keys.session, s);
    },
  });
}

// ---- Connections ----

export function useConnectorKinds() {
  return useQuery({
    queryKey: keys.kinds,
    queryFn: ({ signal }) => unwrap(api.GET("/connector-kinds", { signal })),
    staleTime: Infinity,
  });
}

export function useConnections() {
  return useQuery({
    queryKey: keys.connections,
    queryFn: ({ signal }) => unwrap(api.GET("/connections", { signal })),
  });
}

export function useConnection(id: string | undefined) {
  return useQuery({
    queryKey: keys.connection(id ?? ""),
    queryFn: ({ signal }) =>
      unwrap(api.GET("/connections/{id}", { params: { path: { id: id! } }, signal })),
    enabled: !!id,
  });
}

export function useSaveConnection(id: string | undefined) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ConnectionInput) =>
      id
        ? unwrap(api.PUT("/connections/{id}", { params: { path: { id } }, body }))
        : unwrap(api.POST("/connections", { body })),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.connections });
    },
  });
}

export function useDeleteConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      unwrap(api.DELETE("/connections/{id}", { params: { path: { id } } })),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: keys.connections });
    },
  });
}

export function useTestConnection() {
  return useMutation({
    mutationFn: (body: {
      kind: string;
      config: Config;
      secret?: string | null;
      connectionId?: string;
    }) => unwrap(api.POST("/connections/test", { body })),
  });
}

export function browse(connectionId: string, parent: string, signal?: AbortSignal) {
  return unwrap(
    api.GET("/connections/{id}/browse", {
      params: { path: { id: connectionId }, query: parent ? { parent } : {} },
      signal,
    }),
  );
}

export function useBrowse(connectionId: string, parent: string, enabled = true) {
  return useQuery({
    queryKey: keys.browse(connectionId, parent),
    queryFn: ({ signal }) => browse(connectionId, parent, signal),
    enabled: enabled && !!connectionId,
    staleTime: 60_000,
    retry: false,
  });
}

/**
 * Titles of the pages of a connection that have been browsed so far, by id.
 * Read from the browse cache, so the scope summaries can show names instead
 * of ids without loading anything else.
 */
export function usePageTitles(connectionId: string | undefined) {
  const qc = useQueryClient();
  const [version, setVersion] = useState(0);
  useEffect(() => {
    if (!connectionId) return;
    return qc.getQueryCache().subscribe((e) => {
      const k = e.query.queryKey;
      if (
        e.type === "updated" &&
        k[0] === "connections" &&
        k[1] === connectionId &&
        k[2] === "browse"
      ) {
        setVersion((v) => v + 1);
      }
    });
  }, [qc, connectionId]);
  return useMemo(() => {
    void version;
    const out = new Map<string, string>();
    if (!connectionId) return out;
    for (const [, nodes] of qc.getQueriesData<BrowseNode[]>({
      queryKey: ["connections", connectionId, "browse"],
    })) {
      for (const n of nodes ?? []) out.set(n.id, n.title);
    }
    return out;
  }, [qc, connectionId, version]);
}

// ---- Flows ----

export function useFlows() {
  return useQuery({
    queryKey: keys.flows,
    queryFn: ({ signal }) => unwrap(api.GET("/flows", { signal })),
    // Poll while something runs, so the dashboard follows it.
    refetchInterval: (q) => (q.state.data?.some((f) => f.running) ? 3_000 : 30_000),
  });
}

export function useFlow(id: string | undefined) {
  return useQuery({
    queryKey: keys.flow(id ?? ""),
    queryFn: ({ signal }) =>
      unwrap(api.GET("/flows/{id}", { params: { path: { id: id! } }, signal })),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data?.running ? 3_000 : false),
  });
}

/** A value that follows `value` once it stopped changing for `ms`. */
export function useDebounced<T>(value: T, ms: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return debounced;
}

/** The server's explanation of a schedule: sentence, cron and next runs. */
export function useSchedulePreview(schedule: Schedule, timezone: string) {
  const { i18n } = useTranslation();
  const lang = i18n.resolvedLanguage === "it" ? "it" : "en";
  // Typing a cron expression or a timezone should not send a request per key.
  const key = useDebounced(JSON.stringify({ schedule, timezone }), 300);
  return useQuery({
    queryKey: ["schedule-preview", key, lang],
    queryFn: ({ signal }) => {
      const input = JSON.parse(key) as { schedule: Schedule; timezone: string };
      return unwrap(
        api.POST("/schedules/preview", {
          body: { schedule: input.schedule, timezone: input.timezone || undefined, lang },
          signal,
        }),
      );
    },
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    retry: false,
  });
}

export function useSaveFlow(id: string | undefined) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: FlowInput) =>
      id
        ? unwrap(api.PUT("/flows/{id}", { params: { path: { id } }, body }))
        : unwrap(api.POST("/flows", { body })),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.flows }),
        qc.invalidateQueries({ queryKey: keys.connections }),
      ]);
    },
  });
}

export function useDeleteFlow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => unwrap(api.DELETE("/flows/{id}", { params: { path: { id } } })),
    onSuccess: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.flows }),
        qc.invalidateQueries({ queryKey: keys.connections }),
        qc.invalidateQueries({ queryKey: keys.runs }),
      ]);
    },
  });
}

// ---- Runs ----

export function useStartRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ flowId, dryRun }: { flowId: string; dryRun: boolean }) =>
      unwrap(api.POST("/flows/{id}/runs", { params: { path: { id: flowId } }, body: { dryRun } })),
    onSettled: async () => {
      await Promise.all([
        qc.invalidateQueries({ queryKey: keys.flows }),
        qc.invalidateQueries({ queryKey: keys.runs }),
      ]);
    },
  });
}

export type RunFilters = { flowId?: string; status?: RunStatus; dryRun?: boolean };

export function useRuns(filters: RunFilters, pageSize = 50) {
  return useInfiniteQuery({
    queryKey: keys.runList(filters),
    queryFn: ({ pageParam, signal }) =>
      unwrap(
        api.GET("/runs", {
          params: { query: { ...filters, limit: pageSize, before: pageParam } },
          signal,
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextBefore,
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) => p.items.some((r) => r.status === "running")) ? 3_000 : false,
  });
}

export function useRun(id: string | undefined) {
  return useQuery({
    queryKey: keys.run(id ?? ""),
    queryFn: ({ signal }) =>
      unwrap(api.GET("/runs/{id}", { params: { path: { id: id! } }, signal })),
    enabled: !!id,
  });
}

export function useCancelRun() {
  return useMutation({
    mutationFn: (id: string) => unwrap(api.POST("/runs/{id}/cancel", { params: { path: { id } } })),
  });
}
