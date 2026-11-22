import type { Plugin } from "vite";

const author = { id: 1, username: "GikGok", avatar_url: "/poster.png" };
const videos = [
  ["clip.mp4", "城市里的光 · 竖屏"],
  ["square.mp4", "城市里的光 · 方形"],
  ["landscape.mp4", "城市里的光 · 横屏"],
].map(([file, title], index) => ({
  id: index + 1,
  title,
  author,
  author_id: author.id,
  username: author.username,
  description: "本地演示片源，点击播放。",
  play_url: "/" + file,
  cover_url: "/poster.png",
  create_time: 1720000000,
  likes_count: 0,
  is_liked: false,
}));

export function demoPreview(): Plugin {
  return {
    name: "shortvideo-demo-preview",
    configureServer(server) {
      server.middlewares.use(async (request, response, next) => {
        response.setHeader(
          "Content-Security-Policy",
          "connect-src 'self'; media-src 'self' blob:; img-src 'self' data: blob:",
        );
        const path = request.url?.split("?")[0];
        if (!path?.startsWith("/api/")) return next();

        function send(status: number, body: unknown) {
          response.writeHead(status, {
            "Content-Type": "application/json",
            "Cache-Control": "no-store",
          });
          response.end(JSON.stringify(body));
        }

        if (request.method !== "POST")
          return send(405, { error: "本地预览仅供浏览" });

        switch (path) {
          case "/api/feed/listLatest":
          case "/api/feed/listLikesCount":
          case "/api/feed/listByPopularity":
            return send(200, { video_list: videos, has_more: false });
          case "/api/account/findByID":
            return send(200, author);
          case "/api/account/getProfile":
            return send(200, {
              account: author,
              video_count: videos.length,
              total_likes: 0,
              follower_count: 0,
              vlogger_count: 0,
            });
          case "/api/video/listByAuthorID":
            return send(
              200,
              videos.map((video) => ({
                ...video,
                create_time: "2024-07-03T09:46:40Z",
              })),
            );
          case "/api/comment/listAll":
            return send(200, []);
          case "/api/comment/listThreads":
          case "/api/comment/listReplies":
            return send(200, { comments: [], next_cursor: 0 });
          case "/api/video/getDetail": {
            try {
              const chunks: Buffer[] = [];
              let bytes = 0;
              for await (const chunk of request) {
                const data = Buffer.from(chunk);
                bytes += data.length;
                if (bytes > 4096) return send(413, { error: "请求过大" });
                chunks.push(data);
              }
              const { id } = JSON.parse(Buffer.concat(chunks).toString());
              const video = videos.find((item) => item.id === id);
              return video
                ? send(200, { ...video, create_time: "2024-07-03T09:46:40Z" })
                : send(404, { error: "视频不存在" });
            } catch {
              return send(400, { error: "请求格式错误" });
            }
          }
          default:
            return send(403, { error: "本地预览仅供浏览" });
        }
      });
    },
  };
}
