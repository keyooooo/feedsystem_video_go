import http from "node:http";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
const clipFile = process.env.SMOKE_CLIP_FILE || "/fixtures/clip.mp4";
const clips = {
  "/static/clip.mp4": readFileSync(clipFile),
  "/static/seek.mp4": readFileSync(join(dirname(clipFile), "seek.mp4")),
};
http
  .createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    // Support both direct API access and an already-mounted gateway.
    url.pathname = url.pathname.replace(/^\/api(?=\/)/, "");
    if (url.pathname === "/healthz") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok" }));
      return;
    }
    if (/^\/media\/assets\/[a-f0-9]{32}$/.test(url.pathname)) {
      if (url.searchParams.get("resolve") === "1") {
        res.writeHead(200, {
          "Content-Type": "application/json",
          "Cache-Control": "no-store",
        });
        res.end(
          JSON.stringify({ url: "/static/seek.mp4?mock-signature=fresh" }),
        );
        return;
      }
      res.writeHead(307, {
        Location: "/static/seek.mp4?mock-signature=initial",
        "Cache-Control": "no-store",
      });
      res.end();
      return;
    }
    const clip = clips[url.pathname];
    if (Buffer.isBuffer(clip)) {
      const match = req.headers.range?.match(/bytes=(\d+)-(\d*)/);
      const start = match ? Number(match[1]) : 0,
        end = match?.[2]
          ? Math.min(Number(match[2]), clip.length - 1)
          : clip.length - 1;
      res.writeHead(match ? 206 : 200, {
        "Content-Type": "video/mp4",
        "Accept-Ranges": "bytes",
        "Content-Length": end - start + 1,
        ...(match
          ? { "Content-Range": `bytes ${start}-${end}/${clip.length}` }
          : {}),
      });
      res.end(clip.subarray(start, end + 1));
      return;
    }
    res.writeHead(404);
    res.end();
  })
  .listen(Number(process.env.SMOKE_BACKEND_PORT || 8080), "0.0.0.0");
