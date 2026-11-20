// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { createOSSTransport } from "./ossTransport";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllEnvs();
  vi.unstubAllGlobals();
});

it("signs an OSS multipart initiation flag as a bare query key", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-01-02T03:04:05Z"));
  vi.stubEnv("VITE_OSS_UPLOAD_ORIGIN", "https://fixture-oss-gz.oss-cn-guangzhou.aliyuncs.com");
  const fetchOSS = vi.fn(async (_url: string) =>
    new Response("<InitiateMultipartUploadResult/>"),
  );
  vi.stubGlobal("fetch", fetchOSS);

  const id = "0123456789abcdef0123456789abcdef";
  const send = createOSSTransport({
    bucket: "fixture-oss-gz",
    region: "cn-guangzhou",
    endpoint: "https://oss-cn-guangzhou.aliyuncs.com",
    key: "direct-upload/staging/video/" + id,
    access_key_id: "test-id",
    access_key_secret: "test-secret",
    security_token: "test-token",
    expires_at: "2026-01-02T03:19:05Z",
  }, "video", id);
  await send("POST", new AbortController().signal, { uploads: "" }, undefined, "video/mp4");

  const request = new URL(fetchOSS.mock.calls[0][0]);
  expect(request.searchParams.has("uploads")).toBe(true);
  // V4 output for the synthetic fixture with uploads in the canonical
  // query string. Signing uploads= produces a different signature.
  expect(request.searchParams.get("x-oss-signature")).toBe(
    "776f94d4f5fc5fdf020dc8add3d5c865315273cdc2cd2e924310799429791644",
  );
});
