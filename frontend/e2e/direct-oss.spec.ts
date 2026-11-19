import { readFileSync } from "node:fs";
import { test, expect, type Page } from "@playwright/test";

const oss = process.env.E2E_OSS_UPLOAD_ORIGIN;
const playback = process.env.E2E_OSS_PLAYBACK_ORIGIN;
const clip = readFileSync(new URL("./fixtures/clip.mp4", import.meta.url));
const poster = new URL("./fixtures/poster.png", import.meta.url).pathname;
const partSize = 5 * 1024 * 1024;
const ids = {
  video: "1".repeat(32),
  cover: "2".repeat(32),
  avatar: "3".repeat(32),
};
type Kind = keyof typeof ids;
type Media = { kind: Kind; size: number; content_type: string };
type Options = {
  invalid?: boolean;
  blockComplete?: Promise<void>;
  failSecond?: boolean;
  expire?: boolean;
};

test.skip(!oss || !playback, "Requires the direct-upload frontend image");
test.beforeEach(({ page }) => {
  page.on("console", (message) => {
    if (message.type() === "error") console.error(message.text());
  });
});

async function mockUpload(page: Page, options: Options = {}) {
  const media = new Map<string, Media>();
  const ready = new Set<string>();
  const cancelled: string[] = [];
  const events: string[] = [];
  const parts = new Map<number, number>();
  const sends: number[] = [];
  let active = 0;
  let peak = 0;
  let renewed = 0;
  let committed = 0;
  let started = 0;
  let listed = 0;
  let failSecond = options.failSecond;
  function asset(id: string) {
    return {
      ...media.get(id),
      id,
      status: ready.has(id) ? "ready" : "pending",
      expires_at: new Date(Date.now() + 86400_000).toISOString(),
    };
  }
  function authorization(id: string, token: string) {
    const entry = asset(id);
    return {
      upload: entry,
      grant: {
        key: `direct-upload/staging/${entry.kind}/${id}`,
        bucket: "demo-bucket",
        region: "cn-guangzhou",
        endpoint:
          oss === "https://demo-bucket.oss-cn-guangzhou.aliyuncs.com"
            ? "https://oss-cn-guangzhou.aliyuncs.com"
            : oss,
        access_key_id: "temporary-key",
        access_key_secret: "temporary-secret",
        security_token: token,
        expires_at: new Date(Date.now() + 900_000).toISOString(),
      },
    };
  }
  await page.addInitScript((uploadOrigin) => {
    const claims = btoa(JSON.stringify({ account_id: 8, username: "author" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
    // WebKit's intercepted Request omits Blob bodies. Record the browser input to fetch.
    const uploadBodies = new Map<string, number | string>();
    Object.assign(window, { uploadBodies });
    const fetchRequest = window.fetch;
    window.fetch = function (input, init) {
      if (
        typeof input === "string" &&
        new URL(input, location.href).origin === uploadOrigin
      ) {
        if (init?.body instanceof Blob) uploadBodies.set(input, init.body.size);
        if (typeof init?.body === "string") uploadBodies.set(input, init.body);
      }
      return fetchRequest.call(this, input, init);
    };
  }, oss!);
  const browserBody = (url: string) =>
    page.evaluate(
      (value) =>
        Reflect.get(window, "uploadBodies").get(value) as
          number | string | undefined,
      url,
    );
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith("/api/")) return route.fallback();
    if (path === "/api/uploads") {
      const entry = request.postDataJSON() as Media;
      media.set(ids[entry.kind], entry);
      expect(request.headers().authorization).toMatch(/^Bearer /);
      events.push(`grant:${entry.kind}`);
      await route.fulfill({
        status: 201,
        json: authorization(ids[entry.kind], "initial-token"),
      });
      return;
    }
    const [, id, action] =
      path.match(/^\/api\/uploads\/([a-f0-9]{32})(?:\/(\w+))?$/) ?? [];
    if (id && action === "credentials") {
      renewed++;
      await route.fulfill({ json: authorization(id, "renewed-token") });
      return;
    }
    if (id && action === "cancel") {
      cancelled.push(id);
      await route.fulfill({
        json: { upload: { ...asset(id), status: "cancelled" } },
      });
      return;
    }
    if (id && action === "complete") {
      events.push(`verify:${asset(id).kind}`);
      await options.blockComplete;
      if (options.invalid) {
        await route.fulfill({
          status: 422,
          json: { code: "invalid_media", error: "媒体校验失败" },
        });
        return;
      }
      ready.add(id);
      await route.fulfill({ json: { upload: asset(id) } }).catch(() => {});
      return;
    }
    if (id) {
      await route.fulfill({ json: { upload: asset(id) } });
      return;
    }
    if (path === "/api/video/publish") {
      const body = request.postDataJSON();
      expect(body).toEqual({
        title: "OSS 直传",
        description: "",
        video_asset_id: ids.video,
        cover_asset_id: ids.cover,
      });
      expect(ready.has(ids.video) && ready.has(ids.cover)).toBe(true);
      events.push("publish");
      await route.fulfill({ json: { id: 97, title: body.title } });
      return;
    }
    if (path === "/api/account/findByID") {
      await route.fulfill({ json: { id: 8, username: "author" } });
      return;
    }
    if (path === "/api/account/applyAvatar") {
      expect(request.postDataJSON()).toEqual({ asset_id: ids.avatar });
      expect(ready.has(ids.avatar)).toBe(true);
      events.push("avatar:apply");
      await route.fulfill({
        json: { avatar_url: `/api/media/assets/${ids.avatar}` },
      });
      return;
    }
    expect(path).not.toMatch(/\/video\/(chunk|upload)|\/account\/uploadAvatar/);
    await route.fulfill({ json: {} });
  });

  await page.route(`${oss}/**`, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const q = url.searchParams;
    const method = request.method();
    const headers = {
      "access-control-allow-origin": request.headers().origin || "*",
      "access-control-allow-methods": "GET,POST,PUT,DELETE",
      "access-control-allow-headers": "content-type",
      "access-control-expose-headers": "ETag",
      "content-type": "application/xml",
    };
    if (method === "OPTIONS") {
      await route.fulfill({ status: 204, headers });
      return;
    }
    expect(request.headers()).not.toHaveProperty("authorization");
    expect(q.get("x-oss-signature-version")).toBe("OSS4-HMAC-SHA256");
    expect(q.get("x-oss-signature")).toMatch(/^[a-f0-9]{64}$/);
    expect(q.get("x-oss-security-token")).toMatch(/^(initial|renewed)-token$/);
    const id = url.pathname.split("/").at(-1)!;
    expect(url.pathname).toBe(`/direct-upload/staging/${asset(id).kind}/${id}`);
    if (q.has("uploads")) {
      started++;
      await route.fulfill({
        headers,
        body: "<InitiateMultipartUploadResult><UploadId>multipart-1</UploadId></InitiateMultipartUploadResult>",
      });
      return;
    }
    if (method === "GET") {
      listed++;
      const xml = [...parts]
        .map(
          ([number, size]) =>
            `<Part><PartNumber>${number}</PartNumber><Size>${size}</Size><ETag>&quot;part-${number}&quot;</ETag></Part>`,
        )
        .join("");
      await route.fulfill({
        headers,
        body: `<ListPartsResult><IsTruncated>false</IsTruncated>${xml}</ListPartsResult>`,
      });
      return;
    }
    if (q.has("partNumber")) {
      const number = Number(q.get("partNumber"));
      const size =
        request.postDataBuffer()?.length ?? (await browserBody(request.url()));
      sends.push(number);
      peak = Math.max(peak, ++active);
      try {
        await new Promise((resolve) => setTimeout(resolve, 170));
        if (
          options.expire &&
          q.get("x-oss-security-token") === "initial-token"
        ) {
          await route.fulfill({
            status: 403,
            headers,
            body: "<Error><Code>SecurityTokenExpired</Code></Error>",
          });
          return;
        }
        if (failSecond && number === 2) {
          await route.fulfill({
            status: 503,
            headers,
            body: "<Error><Code>ServiceUnavailable</Code></Error>",
          });
          return;
        }
        expect(size).toBe(
          Math.min(partSize, asset(id).size! - (number - 1) * partSize),
        );
        parts.set(number, Number(size));
        await route.fulfill({
          headers: { ...headers, ETag: `"part-${number}"` },
        });
      } finally {
        active--;
      }
      return;
    }
    if (method === "POST" && q.has("uploadId")) {
      committed++;
      expect(
        request.postData() ?? (await browserBody(request.url())),
      ).toContain('<PartNumber>1</PartNumber><ETag>"part-1"</ETag>');
      await route.fulfill({
        headers,
        body: "<CompleteMultipartUploadResult/>",
      });
      return;
    }
    if (method === "DELETE") {
      await route.fulfill({ status: 204, headers });
      return;
    }
    expect(method).toBe("PUT");
    expect(["cover", "avatar"]).toContain(asset(id).kind);
    expect(
      request.postDataBuffer()?.length ?? (await browserBody(request.url())),
    ).toBe(asset(id).size);
    await route.fulfill({ headers });
  });
  return {
    events,
    sends,
    cancelled,
    metrics: () => ({ peak, renewed, committed, started, listed }),
    recover: () => {
      failSecond = false;
    },
  };
}

async function submit(page: Page, chunks = 1) {
  await page.goto("/video");
  await page.getByLabel("选择视频").setInputFiles({
    name: "fixture.mp4",
    mimeType: "video/mp4",
    buffer: Buffer.concat([clip, Buffer.alloc((chunks - 1) * partSize)]),
  });
  await page.getByLabel("作品封面").setInputFiles(poster);
  await page.getByLabel("作品标题").fill("OSS 直传");
  await page.getByRole("button", { name: "发布作品", exact: true }).click();
}

test("OSS direct upload renews STS once across three workers and publishes only verified assets", async ({
  page,
}) => {
  const mock = await mockUpload(page, { expire: true });
  await submit(page, 4);
  await expect(page.getByRole("link", { name: "查看作品 ↗" })).toHaveAttribute(
    "href",
    "/video/97",
    { timeout: 15_000 },
  );
  expect(mock.metrics()).toMatchObject({
    peak: 3,
    renewed: 1,
    committed: 1,
    started: 1,
  });
  expect(mock.events).toEqual([
    "grant:video",
    "verify:video",
    "grant:cover",
    "verify:cover",
    "publish",
  ]);
  expect(await page.evaluate(() => JSON.stringify(localStorage))).not.toMatch(
    /temporary-(key|secret)|initial-token|renewed-token|oss-upload:v1/,
  );
});

test("OSS direct upload limits retries to three and resumes after reload", async ({
  page,
}) => {
  const mock = await mockUpload(page, { failSecond: true });
  await submit(page, 2);
  await expect(page.getByRole("alert")).toContainText("对象存储请求失败 (503)");
  expect(mock.sends.filter((number) => number === 2)).toHaveLength(3);
  const saved = await page.evaluate(() =>
    Object.entries(localStorage).filter(([key]) =>
      key.startsWith("feedsystem:oss-upload:"),
    ),
  );
  expect(saved).toHaveLength(1);
  expect(saved[0]?.[0]).toContain(":8:video:");
  expect(saved[0]?.[1]).toBe(
    JSON.stringify({ sessionID: ids.video, multipartID: "multipart-1" }),
  );
  mock.recover();
  await submit(page, 2);
  await expect(page.getByRole("link", { name: "查看作品 ↗" })).toBeVisible();
  expect(mock.metrics()).toMatchObject({ committed: 1, started: 1, listed: 1 });
  expect(mock.sends.filter((number) => number === 1)).toHaveLength(1);
});

test("OSS direct upload cancels during a late validation response without publishing", async ({
  page,
}) => {
  let release!: () => void;
  const blockComplete = new Promise<void>((resolve) => {
    release = resolve;
  });
  const mock = await mockUpload(page, { blockComplete });
  await submit(page);
  await expect(page.getByRole("status")).toContainText("正在校验文件");
  await page.getByRole("button", { name: "取消上传", exact: true }).click();
  await page.getByRole("button", { name: "确认取消" }).click();
  await expect.poll(() => mock.cancelled).toEqual([ids.video]);
  release();
  await expect(
    page.getByRole("button", { name: "发布作品", exact: true }),
  ).toBeEnabled();
  expect(mock.events).toEqual(["grant:video", "verify:video"]);
});

test("OSS direct upload displays backend validation error and does not publish", async ({
  page,
}) => {
  const mock = await mockUpload(page, { invalid: true });
  await submit(page);
  await expect(page.getByRole("alert")).toContainText("媒体校验失败");
  expect(mock.events).toEqual(["grant:video", "verify:video"]);
});

test("OSS direct avatar upload uses a separate staged asset and applies after validation", async ({
  page,
}) => {
  const mock = await mockUpload(page);
  await page.goto("/settings");
  await page.getByLabel("上传头像").setInputFiles(poster);
  await page.getByRole("button", { name: "保存资料" }).click();
  await expect(page.getByText("个人资料已保存")).toBeVisible();
  expect(mock.events).toEqual([
    "grant:avatar",
    "verify:avatar",
    "avatar:apply",
  ]);
});

test("OSS direct playback refreshes its stable media URL on an expired seek and preserves pause", async ({
  page,
  request: api,
}) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", {
      value: {
        writeText: () => Promise.reject(new Error("clipboard unavailable")),
      },
    });
  });
  const clip = readFileSync(new URL("./fixtures/seek.mp4", import.meta.url));
  const stablePath = `/api/media/assets/${ids.video}`;
  await page.route("**/api/video/getDetail", (route) =>
    route.fulfill({
      json: {
        id: 97,
        author_id: 8,
        username: "author",
        title: "签名播放",
        play_url: stablePath,
        cover_url: "",
        likes_count: 0,
        description: "",
        create_time: "2026-10-01T12:00:00Z",
      },
    }),
  );
  await page.route("**/api/comment/listAll", (route) =>
    route.fulfill({ json: [] }),
  );
  const refresh: boolean[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === stablePath)
      refresh.push(url.searchParams.has("resolve"));
  });
  if (process.env.E2E_BASE_URL) {
    const range = await api.get(stablePath, {
      headers: { Range: "bytes=0-1" },
    });
    expect(range.status()).toBe(206);
    expect(range.headers()["content-range"]).toBe(`bytes 0-1/${clip.length}`);
  } else {
    await page.route(`**${stablePath}*resolve=1`, (route) =>
      route.fulfill({ json: { url: "/static/seek.mp4?mock-signature=fresh" } }),
    );
    await page.route(
      (url) =>
        (url.pathname === stablePath && !url.searchParams.has("resolve")) ||
        url.pathname === "/static/seek.mp4",
      (route) => {
        const request = route.request();
        const range = request.headers()["range"] || "";
        const [, left, right] = range.match(/^bytes=(\d+)-(\d*)$/) ?? [];
        const start = left ? Number(left) : 0;
        const end = right
          ? Math.min(Number(right), clip.length - 1)
          : clip.length - 1;
        return route.fulfill({
          status: left ? 206 : 200,
          body: clip.subarray(start, end + 1),
          headers: {
            "Content-Type": "video/mp4",
            "Accept-Ranges": "bytes",
            ...(left
              ? { "Content-Range": `bytes ${start}-${end}/${clip.length}` }
              : {}),
          },
        });
      },
    );
  }
  await page.goto("/video/97");
  const player = page.getByLabel("签名播放", { exact: true });
  await expect
    .poll(() => player.evaluate((video: HTMLVideoElement) => video.readyState))
    .toBeGreaterThan(0);
  await player.evaluate((video: HTMLVideoElement) => video.play());
  await player.dispatchEvent("click");
  await expect
    .poll(() => player.evaluate((video: HTMLVideoElement) => video.paused))
    .toBe(true);
  const seek = await player.evaluate((video: HTMLVideoElement) => {
    const states: string[] = [];
    Object.assign(video, { mediaStates: states });
    for (const name of [
      "loadstart",
      "loadedmetadata",
      "seeking",
      "seeked",
      "canplay",
      "error",
    ]) {
      video.addEventListener(name, () => {
        states.push(
          `${name} time=${video.currentTime} ready=${video.readyState} seekable=${video.seekable.length} error=${video.error?.code ?? 0}`,
        );
      });
    }
    const now = Date.now();
    Date.now = () => now + 16 * 60_000;
    return Number((video.duration / 2).toFixed(2));
  });
  await page.getByRole("slider", { name: "播放进度" }).fill(String(seek));
  await expect.poll(() => refresh.includes(true)).toBe(true);
  await expect
    .poll(() => player.evaluate((video: HTMLVideoElement) => video.currentTime))
    .toBeCloseTo(seek, 1)
    .catch(async (error: unknown) => {
      console.error(
        "Media recovery events",
        await player.evaluate((video) => Reflect.get(video, "mediaStates")),
      );
      throw error;
    });
  expect(await player.evaluate((video: HTMLVideoElement) => video.paused)).toBe(
    true,
  );
  await page.getByRole("button", { name: "分享" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("textbox", { name: "视频链接" })).toHaveValue(
    /\/video\/97$/,
  );
});
