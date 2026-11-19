import { beforeEach, afterEach, it, expect, vi } from "vitest";
import { authSession } from "../session/auth";
import { postJson } from "./client";
beforeEach(() => authSession.getState().setTokens("old", "refresh"));
afterEach(() => vi.unstubAllGlobals());
it("shares one refresh across concurrent unauthorized requests", async () => {
  let refreshes = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options: RequestInit) => {
      if (url.endsWith("/refresh")) {
        refreshes++;
        await new Promise((r) => setTimeout(r, 10));
        return Response.json({ token: "new" });
      }
      return options.headers &&
        "Authorization" in options.headers &&
        options.headers.Authorization === "Bearer new"
        ? Response.json({ ok: true })
        : new Response("", { status: 401 });
    }),
  );
  expect(
    await Promise.all([postJson("/one", {}), postJson("/two", {})]),
  ).toEqual([{ ok: true }, { ok: true }]);
  expect(refreshes).toBe(1);
});
it("does not restore a session when logout occurs during refresh", async () => {
  let complete!: (v: Response) => void;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      url.endsWith("/refresh")
        ? new Promise<Response>((r) => {
            complete = r;
          })
        : new Response("", { status: 401 }),
    ),
  );
  const result = postJson("/protected", {}).catch((e) => e);
  await vi.waitFor(() => expect(complete).toBeDefined());
  authSession.getState().clearTokens();
  complete(Response.json({ token: "late" }));
  await result;
  expect(authSession.getState().token).toBeNull();
});
it("rejects protected requests before contacting the server when logged out", async () => {
  authSession.getState().clearTokens();
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  await expect(
    postJson("/protected", {}, { authRequired: true }),
  ).rejects.toMatchObject({ status: 401 });
  expect(fetch).not.toHaveBeenCalled();
});
it("keeps refresh credentials on temporary network failure", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (url.endsWith("/refresh")) throw new TypeError("network offline");
      return new Response("", { status: 401 });
    }),
  );
  await expect(postJson("/protected", {})).rejects.toThrow("network offline");
  expect(authSession.getState().refreshToken).toBe("refresh");
  expect(authSession.getState().token).toBe("old");
});
it("clears definitively rejected refresh credentials and exposes 401", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("", { status: 401 })),
  );
  await expect(postJson("/protected", {})).rejects.toMatchObject({
    status: 401,
  });
  expect(authSession.getState().refreshToken).toBeNull();
});
it("replays a delayed 401 with the token already refreshed by another request", async () => {
  let resolveLate!: (r: Response) => void;
  let refreshes = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, options: RequestInit) => {
      const token = (options.headers as Record<string, string>).Authorization;
      if (url.endsWith("/refresh")) {
        refreshes++;
        return Response.json({ token: "new" });
      }
      if (token === "Bearer new") return Response.json({ ok: true });
      if (url.endsWith("/late"))
        return new Promise<Response>((resolve) => {
          resolveLate = resolve;
        });
      return new Response("", { status: 401 });
    }),
  );
  const late = postJson("/late", {});
  await vi.waitFor(() => expect(resolveLate).toBeDefined());
  await postJson("/fast", {});
  resolveLate(new Response("", { status: 401 }));
  expect(await late).toEqual({ ok: true });
  expect(refreshes).toBe(1);
});
it("bounds hanging requests without revoking the session", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (_url: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener("abort", () =>
            reject(new DOMException("Aborted", "AbortError")),
          );
        }),
    ),
  );
  await expect(postJson("/slow", {}, { timeoutMs: 10 })).rejects.toMatchObject({
    status: 408,
  });
  expect(authSession.getState().token).toBe("old");
});

it("keeps the request deadline active when response headers arrive but the body stalls", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async (_url: string, init: RequestInit) =>
        new Response(
          new ReadableStream({
            start(controller) {
              init.signal?.addEventListener("abort", () =>
                controller.error(new DOMException("Aborted", "AbortError")),
              );
            },
          }),
          { headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  await expect(
    postJson("/slow-body", {}, { timeoutMs: 10 }),
  ).rejects.toMatchObject({ status: 408 });
  expect(authSession.getState().refreshToken).toBe("refresh");
});
