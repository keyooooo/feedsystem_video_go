import { readFileSync } from "node:fs";
import { test, expect } from "@playwright/test";
const items = Array.from({ length: 12 }, (_, i) => ({
  id: i + 1,
  author: { id: 2, username: "摄影日记" },
  title: `城市里的光 ${i + 1}`,
  description: "让每个日常，都成为值得记录的片刻。",
  play_url: "/static/fixture.mp4",
  cover_url: "/static/poster.png",
  likes_count: i + 10,
  is_liked: false,
  create_time: 1720000000,
}));
test.beforeEach(async ({ page }) => {
  await page.route("**/static/**", async (route) => {
    const video = new URL(route.request().url()).pathname.endsWith(".mp4");
    const bytes = readFileSync(
      new URL(
        video ? "./fixtures/clip.mp4" : "./fixtures/poster.png",
        import.meta.url,
      ),
    );
    const range = route.request().headers()["range"];
    const match = range?.match(/bytes=(\d+)-(\d*)/);
    const start = match ? Number(match[1]) : 0,
      end =
        match && match[2]
          ? Math.min(Number(match[2]), bytes.length - 1)
          : bytes.length - 1;
    await route.fulfill({
      status: match ? 206 : 200,
      body: bytes.subarray(start, end + 1),
      headers: {
        "Content-Type": video ? "video/mp4" : "image/png",
        "Accept-Ranges": "bytes",
        ...(match
          ? { "Content-Range": `bytes ${start}-${end}/${bytes.length}` }
          : {}),
      },
    });
  });
  page.on("pageerror", (error) => console.error("PAGE ERROR:", error.message));
  page.on("console", (message) => {
    if (message.type() === "error") console.error("BROWSER:", message.text());
  });
  await page.route("**/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (!path.startsWith("/api/")) {
      await route.continue();
      return;
    }
    let data: unknown = {};
    if (path.startsWith("/api/feed/"))
      data = { video_list: items, has_more: false, next_time: 1 };
    else if (path === "/api/account/getProfile")
      data = {
        account: { id: 2, username: "摄影日记", bio: "发现光，记录生活。" },
        video_count: 12,
        total_likes: 120,
        follower_count: 20,
        vlogger_count: 4,
      };
    else if (path === "/api/video/listByAuthorID")
      data = items.map((i) => ({
        ...i,
        author_id: 2,
        username: "摄影日记",
        create_time: "2026-10-01T12:00:00Z",
      }));
    else if (path === "/api/comment/listAll") data = [];
    await route.fulfill({ json: data });
  });
});
for (const viewport of [
  { width: 390, height: 844 },
  { width: 768, height: 1024 },
  { width: 1440, height: 900 },
  { width: 360, height: 800 },
])
  test(`feed window and responsive layout ${viewport.width}`, async ({
    page,
  }, info) => {
    await page.setViewportSize(viewport);
    await page.goto("/");
    await expect(
      page.getByRole("navigation", { name: "主导航" }),
    ).toBeVisible();
    await expect(page.locator("video")).toHaveCount(2);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await expect
      .poll(() =>
        page
          .locator("video")
          .first()
          .evaluate((v: HTMLVideoElement) => v.currentTime),
      )
      .toBeGreaterThan(0);
    await page.screenshot({
      path: info.outputPath(`home-${viewport.width}.png`),
      fullPage: true,
    });
    const scroller = page.locator("video").first().locator("xpath=../../..");
    await scroller.evaluate((el) => {
      el.scrollTop = el.clientHeight * 5;
      el.dispatchEvent(new Event("scroll"));
    });
    await expect(page.locator("video")).toHaveCount(3);
  });
test("protected publishing retains return URL and password visibility works", async ({
  page,
}) => {
  await page.goto("/video");
  await expect(page).toHaveURL(/account\?redirect=%2Fvideo/);
  await page.getByLabel("密码", { exact: true }).fill("example");
  await page.getByRole("button", { name: "显示密码" }).click();
  await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute(
    "type",
    "text",
  );
});
test("hot ranking and public profile use actual API shapes", async ({
  page,
}, info) => {
  await page.goto("/hot");
  await expect(
    page.getByRole("heading", { name: "此刻，大家都在看" }),
  ).toBeVisible();
  await expect(page.locator("article")).toHaveCount(12);
  await page.screenshot({
    path: info.outputPath("ranking.png"),
    fullPage: true,
  });
  await page.getByRole("link", { name: "推荐", exact: true }).click();
  await expect(
    page.getByRole("tab", { name: "推荐", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await page.goto("/u/2");
  await expect(page.getByRole("heading", { name: "@摄影日记" })).toBeVisible();
  await expect(page.locator("article")).toHaveCount(12);
  await page.screenshot({
    path: info.outputPath("profile.png"),
    fullPage: true,
  });
});
test("feed failure exposes retry without a blank screen", async ({ page }) => {
  await page.route("**/api/feed/**", (route) =>
    route.fulfill({ status: 500, json: { error: "暂时无法加载" } }),
  );
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("暂时无法加载");
  await expect(page.getByRole("button", { name: "重试" })).toBeVisible();
});

async function signedIn(page: import("@playwright/test").Page) {
  await page.addInitScript(() => {
    localStorage.setItem(
      "access_token",
      "header." +
        btoa(JSON.stringify({ account_id: 1, username: "verification" })) +
        ".signature",
    );
    localStorage.setItem("refresh_token", "refresh");
  });
}
test("publishes multipart media through the complete browser workflow", async ({
  page,
}, info) => {
  await signedIn(page);
  const uploaded: number[] = [];
  await page.route("**/api/video/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/video/chunk/init") {
      const data = route.request().postDataJSON();
      expect(data.total_chunks).toBe(2);
      await route.fulfill({
        json: { upload_id: "fixture-session", uploaded_chunks: [] },
      });
      return;
    }
    if (path === "/api/video/chunk/upload") {
      const raw = new Request(route.request().url(), {
        method: "POST",
        headers: route.request().headers(),
        body: new Uint8Array(route.request().postDataBuffer()!).buffer,
      });
      const form = await raw.formData();
      uploaded.push(Number(form.get("chunk_index")));
      expect(form.get("upload_id")).toBe("fixture-session");
      expect(String(form.get("chunk_hash"))).toMatch(/^[a-f0-9]{32}$/);
      await route.fulfill({
        json: { chunk_index: Number(form.get("chunk_index")) },
      });
      return;
    }
    if (path === "/api/video/chunk/complete") {
      expect(uploaded.sort()).toEqual([0, 1]);
      await route.fulfill({ json: { play_url: "/static/fixture.mp4" } });
      return;
    }
    if (path === "/api/video/uploadCover") {
      await route.fulfill({ json: { cover_url: "/static/poster.png" } });
      return;
    }
    if (path === "/api/video/publish") {
      expect(route.request().postDataJSON()).toMatchObject({
        title: "我的第一条作品",
        play_url: "/static/fixture.mp4",
        cover_url: "/static/poster.png",
      });
      await route.fulfill({ json: { id: 77, title: "我的第一条作品" } });
      return;
    }
    await route.fallback();
  });
  await page.goto("/video");
  const clip = readFileSync(new URL("./fixtures/clip.mp4", import.meta.url));
  await page.getByLabel("选择视频").setInputFiles({
    name: "fixture.mp4",
    mimeType: "video/mp4",
    buffer: Buffer.concat([clip, Buffer.alloc(6 * 1024 * 1024)]),
  });
  await page
    .getByLabel("作品封面")
    .setInputFiles(new URL("./fixtures/poster.png", import.meta.url).pathname);
  await page.getByLabel("作品标题").fill("我的第一条作品");
  await page.screenshot({
    path: info.outputPath("publish.png"),
    fullPage: true,
  });
  await page.getByRole("button", { name: "发布作品", exact: true }).click();
  await expect(page.getByRole("link", { name: "查看作品 ↗" })).toBeVisible();
});
test("confirms SPA navigation during upload and cancels instead of publishing", async ({
  page,
}) => {
  await signedIn(page);
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/video/chunk/init", async (route) => {
    await gate;
    try {
      await route.fulfill({
        json: { upload_id: "pending", uploaded_chunks: [] },
      });
    } catch {
      /* request cancelled by navigation */
    }
  });
  await page.goto("/video");
  await page
    .getByLabel("选择视频")
    .setInputFiles(new URL("./fixtures/clip.mp4", import.meta.url).pathname);
  await page
    .getByLabel("作品封面")
    .setInputFiles(new URL("./fixtures/poster.png", import.meta.url).pathname);
  await page.getByLabel("作品标题").fill("取消上传验证");
  await page.getByRole("button", { name: "发布作品", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "取消上传", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "热榜", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "取消当前上传？" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "确认取消" }).click();
  release();
  await expect(page).toHaveURL("/hot");
});
test("keyboard tabs, comment focus and accessibility remain usable", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("tab", { name: "推荐", exact: true }).focus();
  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: "点赞榜" })).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "打开评论" }).first().click();
  await expect(
    page.getByRole("dialog", { name: "评论", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const { default: AxeBuilder } = await import("@axe-core/playwright");
  const result = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21aa"])
    .analyze();
  expect(result.violations).toEqual([]);
});

test("sharing without clipboard permissions exposes the actual video permalink", async ({
  page,
}) => {
  await page.addInitScript(() =>
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error("denied")) },
    }),
  );
  await page.goto("/");
  await page
    .getByRole("button", { name: "分享视频", exact: true })
    .first()
    .click();
  await expect(
    page.getByRole("dialog", { name: "分享视频", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("视频链接")).toHaveValue(
    "http://127.0.0.1:4173/video/1",
  );
});

test("muting preserves the playhead and a user-paused video", async ({
  page,
}) => {
  await page.goto("/");
  const video = page.locator("video").first();
  await expect
    .poll(() => video.evaluate((v: HTMLVideoElement) => v.currentTime))
    .toBeGreaterThan(0.1);
  const pausedAt = await video.evaluate((v: HTMLVideoElement) => {
    v.pause();
    return v.currentTime;
  });
  await page.getByRole("button", { name: "取消静音", exact: true }).click();
  expect(await video.evaluate((v: HTMLVideoElement) => v.paused)).toBe(true);
  expect(
    await video.evaluate((v: HTMLVideoElement) => v.currentTime),
  ).toBeCloseTo(pausedAt, 1);
  expect(await video.evaluate((v: HTMLVideoElement) => v.muted)).toBe(false);
});
