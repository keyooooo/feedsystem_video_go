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
import { AuthPage, Settings } from "./AccountPages";
import { CommentPanel } from "./VideoPages";
import type { FeedVideoItem } from "../api/types";
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
  vi.stubGlobal("matchMedia", () => ({
    matches: true,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
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

it("does not erase a new video comment draft when an old publish finishes", async () => {
  authSession.getState().setTokens("token", "refresh");
  let finish!: () => void;
  server.use(
    http.post("http://localhost:3000/api/comment/listThreads", () =>
      HttpResponse.json({ comments: [], next_cursor: 0 }),
    ),
    http.post(
      "http://localhost:3000/api/comment/publish",
      () =>
        new Promise<Response>((resolve) => {
          finish = () => resolve(HttpResponse.json({ message: "ok" }));
        }),
    ),
  );
  const item = (id: number) =>
    ({
      id,
      title: "video",
      author: { id: 2, username: "author" },
      play_url: "",
      cover_url: "",
      create_time: 0,
      likes_count: 0,
      is_liked: false,
    }) as FeedVideoItem;
  const user = userEvent.setup();
  const view = render(
    <MemoryRouter>
      <CommentPanel item={item(1)} onClose={() => {}} />
    </MemoryRouter>,
  );
  await user.type(screen.getByLabelText("你的评论"), "first draft");
  await user.click(screen.getByRole("button", { name: "发送评论" }));
  await waitFor(() => expect(finish).toBeDefined());
  view.rerender(
    <MemoryRouter>
      <CommentPanel item={item(2)} onClose={() => {}} />
    </MemoryRouter>,
  );
  await user.type(screen.getByLabelText("你的评论"), "second draft");
  await act(async () => finish());
  expect((screen.getByLabelText("你的评论") as HTMLTextAreaElement).value).toBe(
    "second draft",
  );
  expect(
    (screen.getByRole("button", { name: "发送评论" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
});
it("prevents profile loading failures from clearing stored biography", async () => {
  authSession
    .getState()
    .setTokens(
      "header." +
        btoa(JSON.stringify({ account_id: 1, username: "viewer" })) +
        ".sig",
      "refresh",
    );
  server.use(
    http.post("http://localhost:3000/api/account/findByID", () =>
      HttpResponse.json({ error: "profile unavailable" }, { status: 503 }),
    ),
  );
  render(
    <MemoryRouter>
      <Settings />
    </MemoryRouter>,
  );
  await screen.findByRole("alert");
  expect(
    (screen.getByRole("button", { name: "保存资料" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
});

it("clears local credentials even when server logout is unavailable", async () => {
  authSession
    .getState()
    .setTokens(
      "header." +
        btoa(JSON.stringify({ account_id: 1, username: "viewer" })) +
        ".sig",
      "refresh",
    );
  server.use(
    http.post("http://localhost:3000/api/account/findByID", () =>
      HttpResponse.json({ id: 1, username: "viewer", bio: "hello" }),
    ),
    http.post("http://localhost:3000/api/account/logout", () =>
      HttpResponse.json({ error: "offline" }, { status: 503 }),
    ),
  );
  const user = userEvent.setup();
  render(
    <MemoryRouter initialEntries={["/settings"]}>
      <Settings />
      <Location />
    </MemoryRouter>,
  );
  await user.click(screen.getByRole("button", { name: "退出登录" }));
  await user.click(screen.getByRole("button", { name: "确认退出" }));
  await waitFor(() => expect(authSession.getState().token).toBeNull());
  expect(localStorage.getItem("refresh_token")).toBeNull();
  await waitFor(() =>
    expect(screen.getByLabelText("当前位置").textContent).toBe("/account"),
  );
});
