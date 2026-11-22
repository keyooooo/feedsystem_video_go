import { it, expect } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import App from "./App";
it("exposes the navigation and account entrances and marks the active page", () => {
  const html = renderToStaticMarkup(
    <MemoryRouter initialEntries={["/hot"]}>
      <App />
    </MemoryRouter>,
  );
  for (const name of [
    "推荐",
    "热榜",
    "榜单",
    "关注",
    "通知",
    "消息",
    "发布",
    "登录",
  ])
    expect(html).toContain(name);
  expect(html).toMatch(/aria-current="page"[^>]*href="\/hot"/);
  expect(html).toContain("搜索已加载的视频");
});
