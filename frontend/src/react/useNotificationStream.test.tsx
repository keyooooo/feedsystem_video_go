// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { openNotificationStream } from "../api/client";
import { useNotificationStream } from "./useNotificationStream";

vi.mock("../api/client", () => ({ openNotificationStream: vi.fn() }));

it("syncs after a data frame split across chunks, ignores heartbeats and aborts on account change", async () => {
  const encoder = new TextEncoder();
  const streams: ReadableStreamDefaultController<Uint8Array>[] = [];
  vi.mocked(openNotificationStream).mockImplementation(() =>
    Promise.resolve(
      new Response(
        new ReadableStream<Uint8Array>({
          start: (controller) => {
            streams.push(controller);
          },
        }),
        { headers: { "Content-Type": "text/event-stream" } },
      ),
    ),
  );
  const sync = vi.fn();
  const hook = renderHook(({ token }) => useNotificationStream(token, sync), {
    initialProps: { token: "first" },
  });
  const first = vi.mocked(openNotificationStream).mock.calls[0][1];
  await waitFor(() => expect(sync).toHaveBeenCalledTimes(1));
  act(() => {
    streams[0].enqueue(encoder.encode(": keepalive\n\nda"));
    streams[0].enqueue(encoder.encode('ta: {"id":3}\n\n'));
  });
  await waitFor(() => expect(sync).toHaveBeenCalledTimes(2));
  hook.rerender({ token: "second" });
  expect(first.aborted).toBe(true);
  expect(openNotificationStream).toHaveBeenLastCalledWith(
    "second",
    expect.any(AbortSignal),
  );
  hook.unmount();
  streams.forEach((stream) => stream.close());
});
