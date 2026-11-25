import { expect, test } from "@playwright/test";

test("reviewer receives cases missed while the stream was disconnected", async ({ page }) => {
  await page.addInitScript(() => {
    const claims = btoa(JSON.stringify({ account_id: 1, username: "reviewer" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
  });
  let connections = 0;
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/moderation/stream")) {
      connections++;
      if (connections === 1) return route.abort("failed");
      return route.fulfill({ contentType: "text/event-stream", body: ": keepalive\n\n" });
    }
    if (path.endsWith("/moderation/list")) {
      return route.fulfill({ json: {
        cases: connections < 2 ? [] : [{ id: 72, version: 1, state: "awaiting_review" }],
      } });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto("/moderation");
  await expect(page.getByRole("button", { name: /#72/ })).toBeVisible({ timeout: 10000 });
  expect(connections).toBeGreaterThanOrEqual(2);
});

test("reviewer inspects cited evidence and submits a versioned decision", async ({ page }) => {
  await page.addInitScript(() => {
    const claims = btoa(JSON.stringify({ account_id: 1, username: "reviewer" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
    localStorage.setItem("refresh_token", "fixture-refresh");
  });
  const entry = { id: 27, version: 2, state: "awaiting_review" };
  let resolved = false;
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/moderation/stream")) {
      return route.fulfill({ contentType: "text/event-stream", body: ": keepalive\n\n" });
    }
    if (path.endsWith("/moderation/list")) {
      return route.fulfill({ json: { cases: resolved ? [{ ...entry, id: 28 }] : [entry, { ...entry, id: 28 }] } });
    }
    if (path.endsWith("/moderation/detail")) {
      return route.fulfill({ json: {
        case: resolved ? { ...entry, state: "resolved", version: 3 } : entry,
        target_version: 0, reason_code: "spam_promo", detail: "疑似广告",
        comment: { content: "别信楼上的返现广告" },
        decisions: resolved ? [{ id: 1, action: "keep", reason: "回复在提醒读者" }] : [],
        shadow_run_count: 1,
      } });
    }
    if (path.endsWith("/moderation/suggestions")) {
      return route.fulfill({ json: {
        shadow_runs: [{
          id: 8, attempt: 1, model: "test-model", policy_version: "comment-v1",
          fold_candidate: false, execution_outcome: "not_eligible", duration_ms: 120,
          suggestion: { action: "suggest_keep", confidence: 0.98, evidence_ids: ["parent:1"], reason: "引用上文劝阻" },
          evidence: { items: [
            { id: "parent:1", source: "parent", status: "ok", text: "加群领返现" },
            { id: "video:1", source: "video", status: "unavailable" },
          ] },
        }],
      } });
    }
    if (path.endsWith("/moderation/decide")) {
      expect(route.request().postDataJSON()).toMatchObject({
        case_id: 27, case_version: 2, target_version: 0, action: "keep", reason: "回复在提醒读者",
      });
      resolved = true;
      return route.fulfill({ json: { id: 1 } });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto("/moderation");
  await page.getByRole("button", { name: /#27/ }).click();
  await expect(page.getByText("别信楼上的返现广告")).toBeVisible();
  await expect(page.getByText("引用上文劝阻")).toBeHidden();
  await page.getByRole("button", { name: "查看模型建议" }).click();
  await page.getByText("第 1 次审核 · 查看建议与证据").click();
  await expect(page.getByText("引用上文劝阻")).toBeVisible();
  await expect(page.getByText("未达到自动折叠条件，已转人工")).toBeVisible();
  await expect(page.getByText("加群领返现")).toBeVisible();
  await expect(page.getByText("建议引用")).toBeVisible();
  await expect(page.getByText("暂不可用")).toBeVisible();
  await page.getByLabel("审核理由").fill("回复在提醒读者");
  await page.getByRole("button", { name: "保存决定" }).click();
  await expect(page.getByText("维持现状 · 回复在提醒读者")).toBeVisible();
  await expect(page.getByRole("button", { name: "审核下一条" })).toBeVisible();
  await page.getByRole("button", { name: "审核下一条" }).click();
  await expect(page.getByRole("button", { name: /#28/ })).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("审核理由")).toHaveValue("");
});

test("reviewer rechecks changed versions and retries the exact in-doubt decision", async ({ page }) => {
  await page.addInitScript(() => {
    const claims = btoa(JSON.stringify({ account_id: 2, username: "reviewer" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
  });
  let version = 1;
  const submissions: unknown[] = [];
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    const path = new URL(route.request().url()).pathname;
    const item = { id: 33, version, state: "awaiting_review" };
    if (path.endsWith("/moderation/stream")) {
      return route.fulfill({ contentType: "text/event-stream", body: ": keepalive\n\n" });
    }
    if (path.endsWith("/moderation/list")) return route.fulfill({ json: { cases: [item] } });
    if (path.endsWith("/moderation/detail")) return route.fulfill({ json: {
      case: item, target_version: version, comment: { content: `评论版本 ${version}` },
      reason_code: "spam_promo", detail: "", decisions: [],
    } });
    if (path.endsWith("/moderation/decide")) {
      submissions.push(route.request().postDataJSON());
      if (submissions.length === 1) {
        version++;
        return route.fulfill({ status: 500, json: {} });
      }
      return route.fulfill({ json: { id: 19 } });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto("/moderation");
  await page.getByRole("button", { name: /#33/ }).click();
  await expect(page.getByText("评论版本 1")).toBeVisible();
  await page.getByLabel("审核理由").fill("确认保留");
  version = 2;
  await page.getByRole("button", { name: "刷新" }).click();
  await expect(page.getByText("评论版本 2")).toBeVisible();
  const save = page.getByRole("button", { name: "保存决定", exact: true });
  await expect(save).toBeDisabled();
  await page.getByRole("button", { name: "已核对当前版本" }).click();
  await save.click();
  await expect(page.getByText("未确认保存结果，请重试同一决定")).toBeVisible();
  await page.getByRole("button", { name: "刷新" }).click();
  await expect(page.getByText("评论版本 3")).toBeVisible();
  await page.getByRole("button", { name: "重试保存决定" }).click();
  await expect.poll(() => submissions.length).toBe(2);
  expect(submissions[0]).toMatchObject({ case_version: 2, target_version: 2, reason: "确认保留" });
  expect(submissions[1]).toEqual(submissions[0]);
});

test("reporter can reopen a deleted-comment result from a notification", async ({ page }) => {
  await page.addInitScript(() => {
    const claims = btoa(JSON.stringify({ account_id: 1, username: "reporter" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
  });
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    const path = route.request().url();
    if (path.endsWith("/notification/list")) return route.fulfill({ json: { notifications: [{
      id: 1, recipient_id: 1, sender_id: 0, target_id: 95, type: "moderation_report",
      content: "你的举报已处理，作者已删除相关评论", is_read: false, created_at: "2026-11-24T07:00:00Z",
    }] } });
    if (path.endsWith("/notification/unreadCount")) return route.fulfill({ json: { count: 1 } });
    if (path.endsWith("/report/get")) {
      if (route.request().postDataJSON().report_id === 95) return route.fulfill({ json: {
        report_id: 95, state: "resolved", result: "target_removed",
      } });
      return route.fulfill({ status: 404, json: {} });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto("/reports/96");
  await expect(page.getByRole("alert")).toHaveText("找不到这条举报");
  await page.getByRole("button", { name: /^通知/ }).click();
  await page.getByRole("button", { name: /你的举报已处理/ }).click();
  await expect(page).toHaveURL(/\/reports\/95$/);
  await expect(page.getByRole("status").filter({ hasText: "举报已处理，作者已删除评论" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "举报 #95" })).toBeVisible();
  await expect(page.getByText("举报已处理，作者已删除评论", { exact: true })).toBeVisible();
});

test("reviewer must confirm before submitting comment deletion", async ({ page }) => {
  await page.addInitScript(() => {
    const claims = btoa(JSON.stringify({ account_id: 1, username: "reviewer" }));
    localStorage.setItem("access_token", `header.${claims}.signature`);
  });
  const entry = { id: 40, version: 2, state: "awaiting_review" };
  let submitted = false;
  await page.route(url => url.pathname.startsWith("/api/"), async route => {
    switch (new URL(route.request().url()).pathname) {
      case "/api/moderation/stream":
        return route.fulfill({ contentType: "text/event-stream", body: ": keepalive\n\n" });
      case "/api/moderation/list":
        return route.fulfill({ json: { cases: submitted ? [] : [entry] } });
      case "/api/moderation/detail":
        return route.fulfill({ json: {
          case: submitted ? { ...entry, state: "resolved", version: 3 } : entry,
          target_version: 0, comment: submitted ? { removed_at: "2026-11-25", content: "" } : { content: "测试评论" },
          reason_code: "other", detail: "",
          decisions: submitted ? [{ id: 1, action: "delete", reason: "人工删除测试" }] : [],
        } });
      case "/api/moderation/decide":
        expect(route.request().postDataJSON()).toMatchObject({
          case_id: 40, case_version: 2, target_version: 0, action: "delete", reason: "人工删除测试",
        });
        submitted = true;
        return route.fulfill({ json: { id: 1 } });
      default:
        return route.fulfill({ json: {} });
    }
  });
  await page.goto("/moderation");
  await page.getByRole("button", { name: /#40/ }).click();
  await page.getByLabel("处置方式").selectOption("delete");
  await page.getByLabel("审核理由").fill("人工删除测试");
  page.once("dialog", dialog => dialog.dismiss());
  await page.getByRole("button", { name: "保存决定" }).click();
  expect(submitted).toBe(false);
  page.once("dialog", dialog => dialog.accept());
  await page.getByRole("button", { name: "保存决定" }).click();
  await expect(page.getByText("删除评论 · 人工删除测试")).toBeVisible();
});
