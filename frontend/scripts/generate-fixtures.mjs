import { chromium } from "playwright";
import { writeFile } from "node:fs/promises";
const browser = await chromium.launch();
const page = await browser.newPage();
const result = await page.evaluate(async () => {
  const canvas = document.createElement("canvas");
  canvas.width = 360;
  canvas.height = 640;
  const ctx = canvas.getContext("2d");
  const gradient = ctx.createLinearGradient(0, 0, 0, 640);
  gradient.addColorStop(0, "#283b58");
  gradient.addColorStop(0.55, "#cf9478");
  gradient.addColorStop(1, "#101b2b");
  ctx.fillStyle = gradient;
  ctx.fillRect(0, 0, 360, 640);
  ctx.fillStyle = "#f6d8a0";
  ctx.beginPath();
  ctx.arc(260, 180, 46, 0, Math.PI * 2);
  ctx.fill();
  for (let i = 0; i < 12; i++) {
    const height = 100 + ((i * 79) % 220);
    ctx.fillStyle = i % 2 ? "#172330" : "#243244";
    ctx.fillRect(i * 34 - 20, 640 - height, 40, height);
  }
  const poster = canvas.toDataURL("image/png").split(",")[1];
  const type = ["video/mp4;codecs=avc1.42E01E", "video/mp4"].find((t) =>
    MediaRecorder.isTypeSupported(t),
  );
  if (!type) throw Error("MP4 recording unavailable");
  const stream = canvas.captureStream(10),
    recorder = new MediaRecorder(stream, { mimeType: type });
  const chunks = [];
  const done = new Promise((resolve) => {
    recorder.ondataavailable = (e) => chunks.push(e.data);
    recorder.onstop = resolve;
  });
  recorder.start();
  let frame = 0;
  const timer = setInterval(() => {
    ctx.fillStyle = "#5eead4";
    ctx.fillRect(20 + (frame++ % 40) * 2, 600, 3, 3);
  }, 40);
  await new Promise((r) => setTimeout(r, 1600));
  clearInterval(timer);
  recorder.stop();
  await done;
  stream.getTracks().forEach((t) => t.stop());
  const bytes = new Uint8Array(await new Blob(chunks).arrayBuffer());
  let str = "";
  for (const b of bytes) str += String.fromCharCode(b);
  return { poster, video: btoa(str) };
});
await writeFile(
  "e2e/fixtures/poster.png",
  Buffer.from(result.poster, "base64"),
);
await writeFile("e2e/fixtures/clip.mp4", Buffer.from(result.video, "base64"));
await browser.close();
console.log("Generated self-contained synthetic test media.");
