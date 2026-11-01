import http from "node:http";
import { readFileSync } from "node:fs";
const clip = readFileSync("/fixtures/clip.mp4");
http
  .createServer((req, res) => {
    if (req.url === "/healthz") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: "ok" }));
      return;
    }
    if (req.url === "/static/clip.mp4") {
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
  .listen(8080, "0.0.0.0");
