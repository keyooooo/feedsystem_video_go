import { expect, test } from "@playwright/test";

test.skip(
  !!process.env.E2E_BASE_URL,
  "Uses the default local development preview",
);

test("default preview plays local media only after interaction", async ({
  page,
}) => {
  const origin = "http://127.0.0.1:4176";
  const external: string[] = [];
  const media: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.origin !== origin) external.push(request.url());
    if (url.pathname.endsWith(".mp4")) media.push(url.pathname);
  });

  await page.goto(origin);
  await expect(page.getByRole("region", { name: "推荐" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "推荐" })).toHaveCount(0);
  const feed = page.getByRole("region", { name: "视频流" });
  const player = feed.locator("section:not([inert]) [data-player]");
  const video = player.locator("video");
  expect(await video.getAttribute("src")).toBeNull();
  expect(media).toEqual([]);
  await player.getByRole("button", { name: "点击播放", exact: true }).click();
  await expect(player).toHaveAttribute("data-video-shape", "tall");
  await expect
    .poll(() =>
      video.evaluate((element: HTMLVideoElement) => element.currentTime),
    )
    .toBeGreaterThan(0);

  await feed.focus();
  await page.keyboard.press("ArrowDown");
  await expect(
    player.getByRole("heading", { name: "城市里的光 · 方形" }),
  ).toBeVisible();
  await expect(video).not.toHaveAttribute("src");
  expect(new Set(media)).toEqual(new Set(["/clip.mp4"]));
  expect(external).toEqual([]);
  expect(
    (
      await page.request.post(origin + "/api/video/publish", { data: {} })
    ).status(),
  ).toBe(403);
});
