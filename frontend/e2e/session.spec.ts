import { expect, test } from "@playwright/test";

test.skip(
  process.env.E2E_SECURE_SESSIONS !== "1",
  "Requires the cookie-session frontend build",
);

test("resumes a cookie session after reload and honours explicit logout when the API fails", async ({
  page,
}) => {
  let refreshes = 0;
  const token =
    "header." +
    Buffer.from(
      JSON.stringify({ account_id: 55, username: "session-demo" }),
    ).toString("base64url") +
    ".signature";
  await page.addInitScript(() => {
    if (sessionStorage.getItem("legacy-fixture")) return;
    sessionStorage.setItem("legacy-fixture", "1");
    localStorage.setItem("access_token", "legacy-access");
    localStorage.setItem("refresh_token", "legacy-refresh");
  });
  await page.route("**/api/**", (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/account/refresh") {
      expect(request.postDataJSON()).toEqual({});
      expect(request.headers()).not.toHaveProperty("authorization");
      refreshes++;
      return route.fulfill({
        json: { token, account_id: 55, username: "session-demo" },
      });
    }
    if (path === "/api/account/findByID") {
      expect(request.headers().authorization).toBe("Bearer " + token);
      return route.fulfill({
        json: { id: 55, username: "session-demo", bio: "" },
      });
    }
    if (path === "/api/notification/unreadCount") {
      expect(request.headers().authorization).toBe("Bearer " + token);
      return route.fulfill({ json: { count: 0 } });
    }
    if (path === "/api/account/logout") {
      return route.fulfill({
        status: 503,
        json: { error: "service unavailable" },
      });
    }
    return route.fallback();
  });
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "个人设置" })).toBeVisible();
  expect(await page.evaluate(() => Object.keys(localStorage))).not.toContain(
    "refresh_token",
  );
  expect(await page.evaluate(() => Object.keys(localStorage))).not.toContain(
    "access_token",
  );
  await page.reload();
  await expect(page.getByRole("heading", { name: "个人设置" })).toBeVisible();
  expect(refreshes).toBe(2);
  await page.getByRole("button", { name: "退出登录" }).click();
  await page.getByRole("button", { name: "确认退出" }).click();
  await expect(page).toHaveURL("/account");
  await page.goto("/settings");
  await expect(page).toHaveURL(/\/account\?redirect=%2Fsettings$/);
  expect(refreshes).toBe(2);
});
