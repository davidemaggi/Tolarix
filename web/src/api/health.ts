import { useQuery } from "@tanstack/react-query";

export type Health = { status: "ok" | "error"; database: string; version: string };

export async function fetchHealth(signal?: AbortSignal): Promise<Health> {
  const res = await fetch("/healthz", { signal, headers: { Accept: "application/json" } });
  if (!res.ok) throw new Error(`healthz returned ${res.status}`);
  return (await res.json()) as Health;
}

export function useHealth() {
  return useQuery({
    queryKey: ["health"],
    queryFn: ({ signal }) => fetchHealth(signal),
    refetchInterval: 30_000,
    retry: 1,
  });
}
