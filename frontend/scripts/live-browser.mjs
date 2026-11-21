import { chromium } from "playwright";
import { readFileSync, mkdirSync } from "node:fs";
import { spawn } from "node:child_process";
const videoID =
  process.env.LIVE_VIDEO_ID ??
  JSON.parse(readFileSync(".live-report.local", "utf8")).video_id;
if (!/^[1-9]\d*$/.test(String(videoID)))
  throw Error("需要有效的 LIVE_VIDEO_ID");
const base = "http://127.0.0.1:4174";
const server = spawn("npm", ["run", "dev:oss", "--", "--port", "4174"], {
  detached: true,
  stdio: ["ignore", "ignore", "inherit"],
});
let browser;
try {
  let ready = false;
  for (let n = 0; n < 40 && server.exitCode === null; n++) {
    try {
      if ((await fetch(base)).ok) {
        ready = true;
        break;
      }
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  }
  if (!ready) throw Error("真实后端联调预览未能启动");
  browser = await chromium.launch();
  const page = await browser.newPage({
    viewport: { width: 1440, height: 900 },
  });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const detail = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/video/getDetail" &&
      response.request().method() === "POST",
  );
  await page.goto(`${base}/video/${videoID}`);
  const response = await detail;
  if (!response.ok()) throw Error(`视频详情请求失败 (${response.status()})`);
  const { play_url: playURL } = await response.json();
  await page.waitForFunction(
    () => {
      const v = document.querySelector("video");
      return v && v.currentTime > 0.1 && !v.paused;
    },
    {},
    { timeout: 30000 },
  );
  if (new URL(playURL, base).pathname.startsWith("/api/media/assets/")) {
    const range = await page.request.get(new URL(playURL, base).href, {
      headers: { Range: "bytes=0-63" },
      timeout: 15000,
    });
    if (
      range.status() !== 206 ||
      !/^bytes 0-63\/\d+$/.test(range.headers()["content-range"] ?? "") ||
      (await range.body()).length !== 64
    )
      throw Error("OSS 签名媒体未返回预期的 64 字节 Range");
    console.log("PASS: actual signed OSS media Range returned 206.");
  }
  mkdirSync("test-results", { recursive: true });
  await page.screenshot({
    path: "test-results/live-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: "test-results/live-mobile.png",
    fullPage: true,
  });
  if (errors.length) throw Error(errors.join("\n"));
  console.log(
    "PASS: React detail fetch, actual server media playback and desktop/mobile screenshots.",
  );
} finally {
  await browser?.close();
  try {
    process.kill(-server.pid, "SIGTERM");
  } catch {}
}
