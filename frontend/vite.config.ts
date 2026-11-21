import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { demoPreview } from "./demo-preview";
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const demo = mode === "demo";
  const target =
    mode === "oss" || mode === "test" ? env.API_PROXY_TARGET : undefined;
  if (mode === "oss" && !target)
    throw new Error("请先设置 API_PROXY_TARGET，再启动 OSS 联调");
  const upstream = target ? new URL(target) : undefined;
  const apiPrefix = upstream?.pathname.replace(/\/+$/, "") ?? "";
  return {
    plugins: demo ? [react(), demoPreview()] : [react()],
    publicDir: demo ? "e2e/fixtures" : "public",
    ...(demo
      ? {
          cacheDir: "node_modules/.vite-demo",
          define: {
            "import.meta.env.VITE_API_BASE": JSON.stringify("/api"),
            "import.meta.env.VITE_SECURE_SESSIONS": JSON.stringify("false"),
            "import.meta.env.VITE_OSS_DIRECT_UPLOAD": JSON.stringify("false"),
          },
        }
      : {}),
    optimizeDeps: { include: ["ali-oss"] },
    server: {
      host: "127.0.0.1",
      ...(mode === "oss" ? { port: 5174, strictPort: true } : {}),
      proxy: upstream
        ? {
            "/api": {
              target: upstream.origin,
              changeOrigin: true,
              rewrite: (path) => apiPrefix + path.replace(/^\/api(?=\/|$)/, ""),
            },
            "/static": { target: upstream.origin, changeOrigin: true },
          }
        : undefined,
    },
  };
});
