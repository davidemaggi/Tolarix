import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { vi } from "vitest";

import { FakeEventSource, makeEvent, stubEventSource } from "@/test/api";

import { MAX_LINES, useRunStream } from "./run-stream";

const wrapper = ({ children }: { children: React.ReactNode }) => (
  <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider>
);

const nextFrame = () => new Promise((r) => requestAnimationFrame(r));

beforeEach(() => stubEventSource());
afterEach(() => vi.unstubAllGlobals());

describe("useRunStream", () => {
  it("keeps only the last MAX_LINES lines", async () => {
    const { result } = renderHook(() => useRunStream("r1"), { wrapper });
    const es = FakeEventSource.last!;
    await act(async () => {
      for (let i = 1; i <= MAX_LINES + 10; i++) es.emit("log", makeEvent(i, `line ${i}`));
      await nextFrame();
    });
    expect(result.current.events).toHaveLength(MAX_LINES);
    expect(result.current.events[0]!.id).toBe(11);
    expect(result.current.truncated).toBe(true);
  });

  it("reports an error only when the browser gives up", () => {
    const { result } = renderHook(() => useRunStream("r1"), { wrapper });
    const es = FakeEventSource.last!;
    act(() => es.open());
    expect(result.current.state).toBe("live");
    act(() => es.fail());
    expect(result.current.state).toBe("connecting");
    act(() => es.fail(true));
    expect(result.current.state).toBe("error");
  });

  it("closes the connection on unmount", () => {
    const { unmount } = renderHook(() => useRunStream("r1"), { wrapper });
    unmount();
    expect(FakeEventSource.last!.readyState).toBe(FakeEventSource.CLOSED);
  });
});
