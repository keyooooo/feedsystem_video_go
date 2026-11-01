// @vitest-environment jsdom
import { beforeAll, afterAll, afterEach, it, expect, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  renderHook,
  act,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { AuthPage } from "./AccountPages";
import { useLoad } from "./ui";
import { authSession } from "../session/auth";
const server = setupServer(
  http.post("http://localhost:3000/api/account/login", async ({ request }) => {
    const body = (await request.json()) as {
      username: string;
      password: string;
    };
    return body.password === "correct"
      ? HttpResponse.json({ token: "access", refresh_token: "refresh" })
      : HttpResponse.json({ error: "用户名或密码错误" }, { status: 400 });
  }),
);
beforeAll(() => {
  server.listen({ onUnhandledRequest: "error" });
  const intercepted = globalThis.fetch;
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) =>
    intercepted(
      typeof input === "string"
        ? new URL(input, window.location.origin)
        : input,
      init,
    ),
  );
});
afterEach(() => {
  cleanup();
  authSession.getState().clearTokens();
  server.resetHandlers();
});
afterAll(() => {
  vi.unstubAllGlobals();
  server.close();
});
function Location() {
  return <output aria-label="当前位置">{useLocation().pathname}</output>;
}
it("reports backend login errors and then returns the authenticated user to the original page", async () => {
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={["/account?redirect=/messages/2"]}>
      <AuthPage />
      <Location />
    </MemoryRouter>,
  );
  await user.type(screen.getByLabelText("用户名"), "viewer");
  await user.type(screen.getByLabelText("密码", { exact: true }), "wrong");
  await user.click(screen.getByRole("button", { name: /^登录$/ }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("用户名或密码错误");
  await user.clear(screen.getByLabelText("密码", { exact: true }));
  await user.type(screen.getByLabelText("密码", { exact: true }), "correct");
  await user.click(screen.getByRole("button", { name: /^登录$/ }));
  await waitFor(() =>
    expect(screen.getByLabelText("当前位置").textContent).toBe("/messages/2"),
  );
  expect(localStorage.getItem("access_token")).toBe("access");
});
it("ignores out-of-order responses after switching pages", async () => {
  let first!: (v: string) => void, second!: (v: string) => void;
  const a = new Promise<string>((r) => {
      first = r;
    }),
    b = new Promise<string>((r) => {
      second = r;
    });
  const hook = renderHook(({ id }) => useLoad(id, () => (id === "a" ? a : b)), {
    initialProps: { id: "a" },
  });
  hook.rerender({ id: "b" });
  await act(async () => {
    second("new page");
  });
  expect(hook.result.current.data).toBe("new page");
  await act(async () => {
    first("old page");
  });
  expect(hook.result.current.data).toBe("new page");
});
