// @vitest-environment jsdom
import { afterEach, beforeAll, afterAll, expect, it, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { CommentPanel } from "./CommentPanel";
import type { FeedVideoItem } from "../api/types";
const server = setupServer();
beforeAll(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: true,
    addEventListener() {},
    removeEventListener() {},
  }));
  server.listen({ onUnhandledRequest: "error" });
});
afterAll(() => {
  server.close();
  vi.unstubAllGlobals();
});
afterEach(() => {
  cleanup();
  server.resetHandlers();
});

it("hides entire folded rows in one bottom section and reveals only after opening it", async () => {
  const requests: number[] = [];
  const row = (id: number, folded: boolean) => ({
    id,
    author_id: id,
    username: `author-${id}`,
    content: folded ? "" : "普通评论",
    created_at: "2026-11-25T00:00:00Z",
    reply_count: 0,
    moderation_folded: folded,
  });
  server.use(
    http.post("http://localhost:3000/api/comment/listThreads", () =>
      HttpResponse.json({
        comments: [row(3, true), row(2, false), row(1, true)],
        total_count: 3,
      }),
    ),
    http.post(
      "http://localhost:3000/api/comment/reveal",
      async ({ request }) => {
        const { comment_id } = (await request.json()) as { comment_id: number };
        requests.push(comment_id);
        return HttpResponse.json({ content: `已展开正文-${comment_id}` });
      },
    ),
  );
  render(
    <MemoryRouter>
      <CommentPanel item={{ id: 1 } as FeedVideoItem} onClose={() => {}} />
    </MemoryRouter>,
  );
  await screen.findByText("普通评论");
  const toggle = screen.getByRole("button", {
    name: "已折叠评论",
  });
  expect(screen.queryByText("author-3")).toBeNull();
  expect(screen.queryByText("author-1")).toBeNull();
  expect(screen.getAllByRole("article")).toHaveLength(1);
  expect(requests).toEqual([]);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  const user = userEvent.setup();
  await user.click(toggle);
  await screen.findByText("已展开正文-3");
  await screen.findByText("已展开正文-1");
  expect(screen.getAllByRole("article")).toHaveLength(3);
  const group = screen.getByRole("region", { name: "已折叠评论" });
  expect(within(group).getByText("author-3")).toBeTruthy();
  expect(
    screen.getByText("普通评论").compareDocumentPosition(group) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  await user.click(toggle);
  await waitFor(() => expect(screen.queryByText("author-3")).toBeNull());
  expect(screen.getAllByRole("article")).toHaveLength(1);
});
