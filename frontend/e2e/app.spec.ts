import { readFileSync } from "node:fs";
import { test, expect, type Page } from "@playwright/test";
const pageErrors = new WeakMap<Page, string[]>();
const items = Array.from({ length: 12 }, (_, i) => ({
  id: i + 1,
  author: { id: 2, username: "摄影日记" },
  title: `城市里的光 ${i + 1}`,
  description: "让每个日常，都成为值得记录的片刻。",
  play_url:
    i === 0
      ? "http://legacy.example.invalid/static/fixture.mp4"
      : "/static/fixture.mp4",
  cover_url: "/static/poster.png",
  likes_count: i + 10,
  is_liked: false,
  create_time: 1720000000,
}));
test.beforeEach(async ({ page }) => {
  pageErrors.set(page, []);
  if (process.env.E2E_PRODUCTION) {
    await page.addInitScript(() => {
      const violations: string[] = [];
      Object.assign(window, { cspViolations: violations });
      document.addEventListener("securitypolicyviolation", (event) => {
        violations.push(`${event.violatedDirective}: ${event.blockedURI}`);
      });
    });
  }
  await page.route("**/static/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const video = pathname.endsWith(".mp4");
    const fixture = pathname.endsWith("/square.mp4")
      ? "square.mp4"
      : pathname.endsWith("/landscape.mp4")
        ? "landscape.mp4"
        : video
          ? "clip.mp4"
          : "poster.png";
    const bytes = readFileSync(
      new URL(`./fixtures/${fixture}`, import.meta.url),
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
  page.on("pageerror", (error) => pageErrors.get(page)?.push(error.message));
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
    else if (path === "/api/account/findByID") {
      const id = route.request().postDataJSON().id;
      data = {
        id,
        username: id === 2 ? "摄影日记" : "verification",
        avatar_url: "/static/poster.png",
      };
    } else if (path === "/api/account/getProfile")
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
    else if (path === "/api/comment/listThreads")
      data = { comments: [], next_cursor: 0 };
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
    await expect(page).toHaveTitle("GikGok");
    const brand = page.getByRole("link", { name: "GikGok", exact: true });
    await expect(brand).toHaveAttribute("href", "/");
    await expect
      .poll(() =>
        brand
          .locator("img")
          .evaluate((image: HTMLImageElement) => image.naturalWidth),
      )
      .toBeGreaterThan(0);
    const favicon = await page
      .locator('link[rel="icon"]')
      .evaluate((link: HTMLLinkElement) => link.href);
    const iconResponse = await page.request.get(favicon);
    expect(iconResponse.ok()).toBe(true);
    expect(iconResponse.headers()["content-type"]).toContain("image/svg+xml");
    await expect(
      page.getByRole("navigation", { name: "主导航" }),
    ).toBeVisible();
    await expect(page.locator("video")).toHaveCount(2);
    if (viewport.width === 1440) {
      const offset = await page.getByRole("searchbox").evaluate((input) => {
        const form = input.closest("form")!.getBoundingClientRect();
        const header = input.closest("header")!.getBoundingClientRect();
        return (form.left + form.right - header.left - header.right) / 2;
      });
      expect(Math.abs(offset)).toBeLessThan(1);
    }
    const current = page
      .getByRole("region", { name: "视频流" })
      .locator("section:not([inert]) [data-player]");
    await expect(
      current.getByRole("button", { name: "关注", exact: true }),
    ).toHaveCount(0);
    const sizes = await current.evaluate((stage) => {
      const name = stage.querySelector('a[href="/u/2"]')!;
      const title = stage.querySelector("h2")!;
      const description = stage.querySelector("p")!;
      return [name, title, description].map((element) =>
        Number.parseFloat(getComputedStyle(element).fontSize),
      );
    });
    expect(sizes[0]).toBeGreaterThan(sizes[1]!);
    expect(sizes[1]).toBeGreaterThan(sizes[2]!);
    await expect(
      current.getByRole("button", { name: "点赞", exact: true }),
    ).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
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
    const scroller = page.getByRole("region", { name: "视频流" });
    await scroller.evaluate((el) => {
      el.scrollTop = el.clientHeight * 5;
      el.dispatchEvent(new Event("scroll"));
    });
    await expect(page.locator("video")).toHaveCount(3);
  });
test("video detail shares the home controls and carries likes into profile cards", async ({
  page,
}) => {
  await signedIn(page);
  const fixture = {
    ...items[0],
    author_id: 2,
    username: "摄影日记",
    create_time: "2026-10-01T12:00:00Z",
  };
  await page.route("**/api/video/getDetail", (route) =>
    route.fulfill({ json: fixture }),
  );
  await page.route("**/api/like/isLiked", (route) =>
    route.fulfill({ json: { is_liked: false } }),
  );
  await page.route("**/api/video/listByAuthorID", (route) =>
    route.fulfill({
      json: [
        fixture,
        { ...fixture, id: 2, title: "较早点赞的作品", is_liked: true },
      ],
    }),
  );
  await page.goto("/");
  const controls = page.getByRole("group", { name: "视频互动" });
  await controls.getByRole("button", { name: "点赞", exact: true }).click();
  await expect(
    controls.getByRole("button", { name: "取消点赞" }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("link", { name: fixture.title, exact: true }).click();
  await expect(page).toHaveURL("/video/1");
  await expect(
    page.getByRole("heading", { name: fixture.title }),
  ).toBeVisible();
  await expect(
    controls.getByRole("button", { name: "取消点赞" }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("link", { name: "查看 摄影日记 的主页" }).click();
  await expect(page.getByRole("img", { name: /^已赞，/ })).toHaveCount(2);
  await page.getByRole("link", { name: fixture.title, exact: true }).click();
  await controls.getByRole("button", { name: "取消点赞" }).click();
  await expect(
    controls.getByRole("button", { name: "点赞", exact: true }),
  ).toHaveAttribute("aria-pressed", "false");
  await page.getByRole("link", { name: "查看 摄影日记 的主页" }).click();
  await expect(page.getByRole("img", { name: /^未赞，/ })).toHaveCount(1);
  await expect(page.getByRole("img", { name: /^已赞，/ })).toHaveCount(1);
});
test("desktop comments align with both video layouts", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.route("**/api/video/getDetail", (route) =>
    route.fulfill({
      json: {
        ...items[0],
        author_id: 2,
        username: "摄影日记",
        create_time: "2026-10-01T12:00:00Z",
      },
    }),
  );
  for (const path of ["/", "/video/1"]) {
    await page.goto(path);
    if (path === "/")
      await page.getByRole("button", { name: "打开评论" }).click();
    const comments = page.getByRole("complementary", { name: "评论" });
    await expect(comments).toBeVisible();
    const player = page.locator("[data-player]:not(section[inert] *)").first();
    const videoBox = (await player.boundingBox())!;
    const commentBox = (await comments.boundingBox())!;
    expect(Math.abs(videoBox.y - commentBox.y)).toBeLessThan(1);
    expect(Math.abs(videoBox.height - commentBox.height)).toBeLessThan(1);
    expect(commentBox.x).toBeGreaterThan(videoBox.x + videoBox.width);
  }
});
test("protected publishing retains return URL and password visibility works", async ({
  page,
}) => {
  await page.goto("/video");
  await expect(page).toHaveURL(/account\?redirect=%2Fvideo/);
  await expect(
    page
      .getByRole("main")
      .getByText("GikGok", { exact: true })
      .filter({ visible: true }),
  ).toBeVisible();
  await page.getByLabel("密码", { exact: true }).fill("example");
  await page.getByRole("button", { name: "显示密码" }).click();
  await expect(page.getByLabel("密码", { exact: true })).toHaveAttribute(
    "type",
    "text",
  );
  await page.getByRole("link", { name: "GikGok", exact: true }).click();
  await expect(page).toHaveURL("/");
});
test("hot ranking and public profile use actual API shapes", async ({
  page,
}, info) => {
  await page.goto("/hot");
  await expect(page.getByRole("region", { name: "点赞榜" })).toBeVisible();
  await expect(page.locator("article")).toHaveCount(12);
  await page.screenshot({
    path: info.outputPath("ranking.png"),
    fullPage: true,
  });
  await page.getByRole("link", { name: "热榜", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "热榜", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  const author = page.getByRole("link", { name: "查看 摄影日记 的主页" });
  await expect(author.locator("img")).toHaveAttribute(
    "src",
    "/static/poster.png",
  );
  await author.click();
  await expect(page).toHaveURL("/u/2");
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
  await page.getByRole("link", { name: "榜单", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "取消当前上传？" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "确认取消" }).click();
  release();
  await expect(page).toHaveURL("/hot");
});
test("keyboard playback navigation, comment focus and accessibility remain usable", async ({
  page,
}) => {
  await page.goto("/?feed=hot");
  await page.getByRole("region", { name: "视频流" }).focus();
  await page.keyboard.press("ArrowDown");
  await expect(
    page
      .locator("[data-feed-scroll] section:not([inert])")
      .getByRole("heading", { name: "城市里的光 2" }),
  ).toBeVisible();
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
    new URL("/video/1", page.url()).href,
  );
});

test("muting preserves the playhead and a user-paused video", async ({
  page,
}) => {
  await page.goto("/");
  const video = page.locator("video").first();
  await expect(video).toHaveAttribute("src", "/static/fixture.mp4");
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

test("local search does not scan subsequent feed pages without an explicit request", async ({
  page,
}) => {
  let nextPages = 0;
  await page.route("**/api/feed/listLatest", async (route) => {
    const next = route.request().postDataJSON().latest_time > 0;
    if (next) nextPages++;
    await route.fulfill({
      json: { video_list: items, has_more: !next, next_time: 60 },
    });
  });
  await page.goto("/?q=no-matching-title");
  await expect(
    page.getByRole("heading", { name: "没有找到匹配的视频" }),
  ).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect(nextPages).toBe(0);
  await page.getByRole("button", { name: "继续查找" }).click();
  await expect.poll(() => nextPages).toBe(1);
});

test("hot feed retries the database cursor and preserves the current slide", async ({
  page,
}) => {
  const cursor = {
    latest_popularity: 0,
    latest_before: "2026-11-20T13:32:00Z",
    latest_id_before: 4,
  };
  let attempts = 0;
  await page.route("**/api/feed/listByPopularity", async (route) => {
    const request = route.request().postDataJSON();
    if (!request.latest_id_before) {
      await route.fulfill({
        json: {
          video_list: items.slice(0, 4),
          has_more: true,
          as_of: 0,
          next_offset: 0,
          next_latest_popularity: cursor.latest_popularity,
          next_latest_before: cursor.latest_before,
          next_latest_id_before: cursor.latest_id_before,
        },
      });
      return;
    }
    expect(request).toEqual({ limit: 10, as_of: 0, offset: 0, ...cursor });
    attempts++;
    if (attempts === 1) {
      await route.fulfill({ status: 503, json: { error: "热榜暂时无法加载" } });
    } else {
      await route.fulfill({
        json: { video_list: items.slice(3, 5), has_more: false },
      });
    }
  });
  await page.goto("/?feed=hot");
  const feed = page.getByRole("region", { name: "视频流" });
  await feed.focus();
  await page.keyboard.press("ArrowDown");
  await expect(page.getByRole("alert")).toContainText("热榜暂时无法加载");
  await expect(
    page
      .locator("[data-feed-scroll] section:not([inert])")
      .getByRole("heading", { name: "城市里的光 2" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "重试" }).click();
  await expect.poll(() => attempts).toBe(2);
  await expect(
    page
      .locator("[data-feed-scroll] section:not([inert])")
      .getByRole("heading", { name: "城市里的光 2" }),
  ).toBeVisible();
  await feed.focus();
  for (const rank of [3, 4, 5]) {
    await page.keyboard.press("ArrowDown");
    await expect(
      feed
        .locator("section:not([inert])")
        .getByRole("heading", { name: `城市里的光 ${rank}` }),
    ).toBeVisible();
  }
  await expect(
    page.getByRole("heading", { name: "城市里的光 5" }),
  ).toBeVisible();
});

test("player adapts its stage to real portrait, square and landscape videos", async ({
  page,
}, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const shapes = ["tall", "square", "wide"] as const;
  const sources = [
    "/static/fixture.mp4",
    "/static/square.mp4",
    "/static/landscape.mp4",
  ];
  await page.route("**/api/feed/listLatest", (route) =>
    route.fulfill({
      json: {
        video_list: items
          .slice(0, 3)
          .map((item, index) => ({ ...item, play_url: sources[index] })),
        has_more: false,
      },
    }),
  );
  await page.goto("/");
  const feed = page.getByRole("region", { name: "视频流" });
  const stage = feed.locator("section:not([inert]) [data-player]");
  const video = stage.locator("video");
  await expect(stage).toHaveAttribute("data-video-shape", "tall");
  const height = (await stage.boundingBox())!.height;
  for (const shape of shapes) {
    if (shape !== "tall") {
      await feed.focus();
      await page.keyboard.press("ArrowDown");
    }
    await expect(stage).toHaveAttribute("data-video-shape", shape);
    expect((await stage.boundingBox())!.height).toBe(height);
    await expect(video).toHaveCSS("object-fit", "contain");
    await expect(stage.locator('img[aria-hidden="true"]')).toHaveCount(
      shape === "wide" ? 0 : 1,
    );
  }
  // Sample both edges before the smooth scroll can move the slide again.
  const { videoBottom, actionsTop } = await stage.evaluate((element) => ({
    videoBottom: element.querySelector("video")!.getBoundingClientRect().bottom,
    actionsTop: element
      .querySelector('[role="group"][aria-label="视频互动"]')!
      .getBoundingClientRect().top,
  }));
  expect(actionsTop).toBeGreaterThanOrEqual(videoBottom);
  await feed.blur();
  await page.screenshot({ path: info.outputPath("home-mobile-landscape.png") });
});

test("uses distinct feeds for the four sidebar destinations", async ({
  page,
}) => {
  await signedIn(page);
  const latest = page.waitForRequest("**/api/feed/listLatest");
  await page.goto("/");
  expect((await latest).postDataJSON()).toEqual({ limit: 10, latest_time: 0 });
  const nav = page.getByRole("navigation", { name: "主导航" });
  expect(await nav.getByRole("link").allTextContents()).toEqual([
    "推荐",
    "热榜",
    "榜单",
    "关注",
  ]);
  for (const [name, endpoint] of [
    ["热榜", "listByPopularity"],
    ["榜单", "listLikesCount"],
    ["关注", "listByFollowing"],
  ]) {
    const request = page.waitForRequest(`**/api/feed/${endpoint}`);
    await nav.getByRole("link", { name, exact: true }).click();
    await request;
    await expect(page.getByRole("heading", { level: 1 })).toHaveCount(0);
    await expect(nav.getByRole("link", { name, exact: true })).toHaveAttribute(
      "aria-current",
      "page",
    );
  }
});

test("signed-in notification badge clears after marking a notification read", async ({
  page,
}) => {
  await signedIn(page);
  let read = false;
  await page.route("**/api/notification/*", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.endsWith("/stream")) {
      await route.fulfill({
        contentType: "text/event-stream",
        body: ": keepalive\n\n",
      });
    } else if (pathname.endsWith("/unreadCount")) {
      await route.fulfill({ json: { count: read ? 0 : 1 } });
    } else if (pathname.endsWith("/list")) {
      await route.fulfill({
        json: {
          notifications: [
            {
              id: 17,
              content: "有人点赞了你的作品",
              is_read: read,
              created_at: "2026-11-20T13:32:00Z",
            },
          ],
        },
      });
    } else {
      expect(route.request().postDataJSON()).toEqual({ id: 17 });
      read = true;
      await route.fulfill({ json: { message: "ok" } });
    }
  });
  await page.goto("/");
  await expect(
    page.getByRole("link", { name: "我的", exact: true }).locator("img"),
  ).toBeVisible();
  await page.getByRole("button", { name: "通知，1 条未读" }).click();
  const dialog = page.getByRole("dialog", { name: "通知" });
  await dialog.getByRole("button", { name: /有人点赞了你的作品/ }).click();
  await expect(dialog.getByText("有人点赞了你的作品")).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: /有人点赞了你的作品/ }),
  ).toHaveCount(0);
  await dialog.getByRole("button", { name: "关闭" }).click();
  await expect(
    page.getByRole("button", { name: "通知", exact: true }),
  ).toBeVisible();
});

test("the guest following entry carries its return URL into sign-in", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("link", { name: "登录", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "关注", exact: true }).click();
  await expect(page).toHaveURL("/account?redirect=%2F%3Ffeed%3Dfollowing");
});

// Every browser flow also runs against the actual Nginx image, with its enforced CSP.
test.afterEach(async ({ page }) => {
  expect(pageErrors.get(page)).toEqual([]);
  if (process.env.E2E_PRODUCTION && !page.isClosed()) {
    const violations = await page.evaluate(
      () =>
        (window as unknown as { cspViolations?: string[] }).cspViolations ?? [],
    );
    expect(violations).toEqual([]);
  }
});

test("likes preserve state on failure and comments can retry and delete", async ({
  page,
}) => {
  await signedIn(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  let likes = 0,
    publishes = 0;
  let comments: {
    id: number;
    author_id: number;
    username: string;
    content: string;
    created_at: string;
  }[] = [];
  await page.route("**/api/like/like", async (route) => {
    likes++;
    await route.fulfill(
      likes === 1
        ? { status: 503, json: { error: "点赞暂不可用" } }
        : { json: { message: "ok" } },
    );
  });
  await page.route("**/api/comment/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/listThreads")) {
      await route.fulfill({ json: { comments, next_cursor: 0 } });
      return;
    }
    if (path.endsWith("/publish")) {
      publishes++;
      if (publishes === 1) {
        await route.fulfill({ status: 503, json: { error: "评论暂不可用" } });
        return;
      }
      comments = [
        {
          id: 91,
          author_id: 1,
          username: "verification",
          content: route.request().postDataJSON().content,
          created_at: "2026-11-19T08:00:00Z",
        },
      ];
    } else if (path.endsWith("/delete")) {
      expect(route.request().postDataJSON()).toEqual({ comment_id: 91 });
      comments = [];
    }
    await route.fulfill({ json: { message: "ok" } });
  });
  await page.goto("/");
  const like = page.getByRole("button", { name: "点赞", exact: true }).first();
  await like.click();
  await expect(page.getByRole("status")).toHaveText("点赞暂不可用");
  await expect(like).toHaveAttribute("aria-pressed", "false");
  await like.click();
  await expect(
    page.getByRole("button", { name: "取消点赞", exact: true }).first(),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "打开评论" }).first().click();
  const panel = page.getByRole("complementary", { name: "评论" });
  const { width, gap } = await panel.evaluate((element) => {
    const sidebar = element.getBoundingClientRect();
    const feed = document.querySelector("[data-feed-scroll]")!;
    return {
      width: sidebar.width,
      gap: sidebar.left - feed.getBoundingClientRect().right,
    };
  });
  expect(width).toBeGreaterThan(440);
  expect(gap).toBeGreaterThanOrEqual(16);
  await page.getByLabel("你的评论").fill("我的评论");
  await page.getByRole("button", { name: "发送评论" }).click();
  await expect(page.getByRole("alert")).toHaveText("评论暂不可用");
  await expect(page.getByLabel("你的评论")).toHaveValue("我的评论");
  await page.getByRole("button", { name: "发送评论" }).click();
  await expect(
    page.locator("article").filter({ hasText: "我的评论" }),
  ).toBeVisible();
  const deleteButton = panel.getByRole("button", { name: "删除", exact: true });
  await deleteButton.click();
  const confirm = panel.getByRole("dialog", { name: "删除这条评论？" });
  await expect(
    confirm.getByRole("button", { name: "取消", exact: true }),
  ).toBeFocused();
  const outer = (await panel.boundingBox())!;
  const inner = (await confirm.boundingBox())!;
  expect(inner.width).toBeLessThan(outer.width);
  expect(
    Math.abs(inner.x + inner.width / 2 - outer.x - outer.width / 2),
  ).toBeLessThan(1);
  expect(
    Math.abs(inner.y + inner.height / 2 - outer.y - outer.height / 2),
  ).toBeLessThan(1);
  await page.keyboard.press("Escape");
  await expect(confirm).toHaveCount(0);
  await expect(deleteButton).toBeFocused();
  await deleteButton.click();
  await confirm.getByRole("button", { name: "确认删除" }).click();
  await expect(panel.getByLabel("你的评论")).toBeFocused();
  await expect(page.getByRole("heading", { name: "还没有评论" })).toBeVisible();
});

test("profile follow and unfollow reflect accepted changes", async ({
  page,
}) => {
  await signedIn(page);
  const operations: string[] = [];
  await page.route("**/api/social/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/getAllVloggers")) {
      await route.fulfill({ json: { vloggers: [] } });
      return;
    }
    operations.push(path);
    expect(route.request().postDataJSON()).toEqual({ vlogger_id: 2 });
    await route.fulfill({ json: { message: "ok" } });
  });
  await page.goto("/u/2");
  await page.getByRole("button", { name: "关注", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "已关注", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "已关注", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "关注", exact: true }),
  ).toHaveAttribute("aria-pressed", "false");
  expect(operations).toEqual(["/api/social/follow", "/api/social/unfollow"]);
});

test("messages respect IME and line breaks and retain failed drafts for retry", async ({
  page,
}) => {
  await signedIn(page);
  let sends = 0;
  await page.route("**/api/account/findByID", (route) =>
    route.fulfill({ json: { id: 2, username: "friend" } }),
  );
  await page.route("**/api/message/list", (route) =>
    route.fulfill({ json: { messages: [] } }),
  );
  await page.route("**/api/message/send", async (route) => {
    sends++;
    const body = route.request().postDataJSON();
    expect(body).toEqual({ to_id: 2, content: "你好\n第二行" });
    await route.fulfill(
      sends === 1
        ? { status: 503, json: { error: "消息暂不可用" } }
        : {
            json: {
              id: 31,
              from_id: 1,
              to_id: 2,
              content: body.content,
              created_at: "2026-11-19T08:00:00Z",
            },
          },
    );
  });
  await page.goto("/messages/2");
  const composer = page.getByRole("textbox", { name: "消息内容" });
  await expect(page.getByRole("link", { name: "@friend" })).toBeVisible();
  await composer.fill("你好");
  await composer.dispatchEvent("keydown", { key: "Enter", isComposing: true });
  await composer.press("Shift+Enter");
  await expect(composer).toHaveValue("你好\n");
  expect(sends).toBe(0);
  await composer.fill("你好\n第二行");
  await composer.press("Enter");
  await expect(page.getByRole("alert")).toHaveText("消息暂不可用");
  await expect(composer).toHaveValue("你好\n第二行");
  await composer.press("Enter");
  await expect(composer).toHaveValue("");
  await expect(page.getByText("你好 第二行", { exact: false })).toBeVisible();
  expect(sends).toBe(2);
});

test("threaded comments page replies and retain a failed nested reply draft", async ({
  page,
}) => {
  await signedIn(page);
  const time = "2026-11-19T08:00:00Z";
  const root = {
    id: 50,
    video_id: 1,
    author_id: 1,
    username: "楼主",
    content: "一级评论",
    created_at: time,
    reply_count: 2,
    avatar_url: "/static/poster.png",
    likes_count: 3,
    is_liked: false,
  };
  const first = {
    id: 51,
    video_id: 1,
    author_id: 3,
    username: "第一位",
    content: "第一条回复",
    created_at: time,
    root_comment_id: 50,
    reply_to_comment_id: 50,
    reply_to_username: "楼主",
    reply_target_deleted: false,
  };
  const second = {
    ...first,
    id: 52,
    author_id: 1,
    username: "第二位",
    content: "第二条回复",
  };
  const nested = {
    ...first,
    id: 53,
    author_id: 1,
    username: "verification",
    content: "楼中楼",
    reply_to_comment_id: 52,
    reply_to_username: "第二位",
  };
  let attempts = 0;
  let removedRoot = false;
  await page.route("**/api/comment/listThreads", (route) =>
    route.fulfill({
      json: {
        comments: removedRoot ? [] : [root],
        next_cursor: 0,
        total_count: removedRoot ? 0 : 3,
      },
    }),
  );
  await page.route("**/api/comment/listReplies", (route) => {
    const cursor = route.request().postDataJSON().cursor;
    return route.fulfill({
      json: cursor
        ? { comments: [second], next_cursor: 0 }
        : { comments: [first], next_cursor: first.id },
    });
  });
  await page.route("**/api/comment/publish", (route) => {
    expect(route.request().postDataJSON()).toEqual({
      video_id: 1,
      content: "楼中楼",
      reply_to_comment_id: second.id,
    });
    attempts++;
    return attempts === 1
      ? route.fulfill({ status: 503, json: { error: "暂时无法回复" } })
      : route.fulfill({ json: { message: "ok", comment: nested } });
  });
  await page.route("**/api/comment/delete", (route) => {
    const { comment_id } = route.request().postDataJSON();
    expect([root.id, second.id]).toContain(comment_id);
    removedRoot = comment_id === root.id;
    return route.fulfill({
      json: { message: "ok", deleted_count: removedRoot ? 3 : 1 },
    });
  });
  let likeRequests = 0;
  await page.route("**/api/comment/setLike", (route) => {
    const { comment_id, liked } = route.request().postDataJSON();
    expect(comment_id).toBe(root.id);
    expect(liked).toBe(likeRequests < 2);
    return ++likeRequests === 1
      ? route.fulfill({ status: 503, json: { error: "点赞暂不可用" } })
      : route.fulfill({
          json: { likes_count: liked ? 4 : 3, is_liked: liked },
        });
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("button", { name: "打开评论" }).first().click();
  const rootRow = page.locator("article").filter({ hasText: root.content });
  await expect(
    rootRow.getByRole("link", { name: "查看 楼主 的主页" }).locator("img"),
  ).toBeVisible();
  const likeComment = rootRow.getByRole("button", {
    name: "点赞评论",
    exact: true,
  });
  await likeComment.click();
  await expect(page.getByRole("alert")).toHaveText("点赞暂不可用");
  await expect(likeComment).toHaveText("3");
  await likeComment.click();
  const unlikeComment = rootRow.getByRole("button", {
    name: "取消评论点赞",
    exact: true,
  });
  await expect(unlikeComment).toHaveText("4");
  await unlikeComment.click();
  await expect(likeComment).toHaveText("3");
  await page.getByRole("button", { name: "展开 2 条回复" }).click();
  await expect(page.getByText("第一条回复", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "加载更多回复" }).click();
  await page
    .locator("article")
    .filter({ hasText: "第二条回复" })
    .getByRole("button", { name: "回复", exact: true })
    .click();
  const input = page.getByRole("textbox", { name: "你的评论" });
  await expect(input).toHaveAttribute("placeholder", "回复 @第二位");
  await input.fill("楼中楼");
  const send = page.getByRole("button", { name: "发送回复" });
  await send.click();
  await expect(page.getByRole("alert")).toHaveText("暂时无法回复");
  await expect(input).toHaveValue("楼中楼");
  await send.click();
  await expect(page.getByText("楼中楼", { exact: false })).toBeVisible();
  await expect(input).toHaveValue("");
  const thread = page.locator("[data-comment-root='50']");
  const foldAfterReplies = await thread.evaluate((element) => {
    const lastReply = [...element.querySelectorAll("article")].at(-1)!;
    const fold = [...element.querySelectorAll("button")].find((button) =>
      button.textContent?.includes("收起回复"),
    )!;
    return !!(
      lastReply.compareDocumentPosition(fold) & Node.DOCUMENT_POSITION_FOLLOWING
    );
  });
  expect(foldAfterReplies).toBe(true);
  await page.getByRole("button", { name: "收起回复" }).click();
  await expect(
    page.getByRole("button", { name: "展开 3 条回复" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "展开 3 条回复" }).click();
  await page
    .locator("article")
    .filter({ hasText: second.content })
    .getByRole("button", { name: "删除", exact: true })
    .click();
  await page.getByRole("button", { name: "确认删除" }).click();
  await expect(page.getByText(second.content, { exact: true })).toHaveCount(0);
  await expect(
    page.getByText("@第二位（评论已删除）", { exact: true }),
  ).toBeVisible();
  await rootRow.getByRole("button", { name: "删除", exact: true }).click();
  await page.getByRole("button", { name: "确认删除" }).click();
  await expect(thread).toHaveCount(0);
  await expect(page.getByText("楼中楼", { exact: true })).toHaveCount(0);
  await expect(page.getByText("还没有评论")).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "打开评论" }).first().click();
  await expect(page.getByText("还没有评论")).toBeVisible();
});

test("folded comments hide complete rows behind one bottom disclosure", async ({ page }) => {
  test.setTimeout(60000);
  const time = "2026-11-25T08:00:00Z";
  const row = (id: number, folded: boolean) => ({
    id, video_id: 1, author_id: id, username: `审核样例作者${id}`,
    content: folded ? "" : "正常评论保留展示", created_at: time,
    reply_count: id === 50 ? 2 : 0, moderation_folded: folded,
  });
  let reveals: number[] = [];
  await page.route("**/api/comment/listThreads", route => route.fulfill({ json: {
    comments: [row(92, true), row(50, false), row(91, true)], next_cursor: 0, total_count: 5,
  } }));
  await page.route("**/api/comment/listReplies", route => route.fulfill({ json: {
    comments: [
      { ...row(52, true), root_comment_id: 50, reply_to_comment_id: 50, reply_to_username: "审核样例作者50" },
      { ...row(53, false), content: "正常回复", root_comment_id: 50, reply_to_comment_id: 50, reply_to_username: "审核样例作者50" },
    ], next_cursor: 0,
  } }));
  await page.route("**/api/comment/reveal", route => {
    const id = route.request().postDataJSON().comment_id as number;
    reveals.push(id);
    return route.fulfill({ json: { content: `折叠正文${id}` } });
  });
  for (const width of [360, 390, 768, 1440]) {
    reveals = [];
    await page.setViewportSize({ width, height: width >= 1200 ? 900 : 844 });
    await page.goto("/");
    await page.getByRole("button", { name: "打开评论" }).first().click();
    await expect(page.getByText("正常评论保留展示")).toBeVisible();
    const toggle = page.getByRole("button", { name: "已折叠评论", exact: true });
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(page.getByText("审核样例作者92", { exact: true })).toHaveCount(0);
    await expect(page.getByText("审核样例作者91", { exact: true })).toHaveCount(0);
    expect(reveals).toEqual([]);
    await page.getByRole("button", { name: /展开 2 条回复/ }).click();
    await expect(page.getByText("正常回复", { exact: true })).toBeVisible();
    await expect(page.getByText("审核样例作者52", { exact: true })).toHaveCount(0);
    expect(reveals).toEqual([]);
    const region = page.getByRole("region", { name: "已折叠评论" });
    const ordinary = page.getByText("正常评论保留展示");
    expect(await ordinary.evaluate((element, group) => !!(element.compareDocumentPosition(group as Node) & Node.DOCUMENT_POSITION_FOLLOWING), await region.elementHandle())).toBe(true);
    await page.screenshot({ path: `/tmp/folded-comments-${width}-closed.png` });
    await toggle.click();
    await expect(region.getByText("折叠正文92", { exact: true })).toBeVisible();
    await expect(region.getByText("折叠正文91", { exact: true })).toBeVisible();
    await expect(region.getByText("折叠正文52", { exact: true })).toBeVisible();
    expect([...new Set(reveals)].sort()).toEqual([52, 91, 92]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: `/tmp/folded-comments-${width}-open.png` });
    await toggle.focus();
    await toggle.press("Enter");
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(region.getByText("审核样例作者92", { exact: true })).toHaveCount(0);
  }
});
