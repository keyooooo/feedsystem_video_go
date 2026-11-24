import { useEffect } from "react";
import { openNotificationStream } from "../api/client";

export function useNotificationStream(token: string, sync: () => void, openStream = openNotificationStream) {
  useEffect(() => {
    if (!token) return;
    let stopped = false;
    let retry: ReturnType<typeof setTimeout>;
    let refresh: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController;
    let delay = 1000;

    function scheduleSync() {
      if (refresh !== undefined || stopped) return;
      refresh = setTimeout(() => {
        refresh = undefined;
        sync();
      }, 250);
    }

    async function connect() {
      controller = new AbortController();
      const { signal } = controller;
      const expire = () => controller.abort();
      let watchdog = setTimeout(expire, 45000);
      try {
        const response = await openStream(token, signal);
        if (
          !response.ok ||
          !response.body ||
          !response.headers.get("content-type")?.startsWith("text/event-stream")
        ) {
          await response.body?.cancel();
          return;
        }
        if (stopped) return;
        scheduleSync();
        const reader = response.body.getReader();
        try {
          const decoder = new TextDecoder();
          let buffer = "";
          while (!stopped) {
            const { done, value } = await reader.read();
            if (done) break;
            delay = 1000;
            clearTimeout(watchdog);
            watchdog = setTimeout(expire, 45000);
            buffer += decoder.decode(value, { stream: true });
            // SSEHub emits newline-delimited data and comment frames.
            let newline = buffer.indexOf("\n");
            while (newline >= 0) {
              if (buffer.startsWith("data:")) scheduleSync();
              buffer = buffer.slice(newline + 1);
              newline = buffer.indexOf("\n");
            }
            if (buffer.length > 65536) break;
          }
        } finally {
          controller.abort();
          reader.releaseLock();
        }
      } catch {
        // Reconnect after timeouts and transient network failures.
      } finally {
        clearTimeout(watchdog);
        if (!stopped) {
          // The authenticated snapshot also renews expired access tokens.
          scheduleSync();
          retry = setTimeout(() => void connect(), delay);
          delay = Math.min(delay * 2, 30000);
        }
      }
    }

    void connect();
    // Mentions are persisted directly and may arrive outside SSEHub.
    const poll = setInterval(scheduleSync, 30000);
    window.addEventListener("focus", scheduleSync);
    return () => {
      stopped = true;
      controller.abort();
      clearTimeout(retry);
      clearTimeout(refresh);
      clearInterval(poll);
      window.removeEventListener("focus", scheduleSync);
    };
  }, [token, sync, openStream]);
}
