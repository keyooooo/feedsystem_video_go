import { it, expect } from "vitest";
import { safeRedirect } from "./AccountPages";
it("keeps internal return destinations and rejects external login redirects", () => {
  expect(safeRedirect("/messages/7?q=hi")).toBe("/messages/7?q=hi");
  for (const target of [
    "//evil.test",
    "https://evil.test",
    "/\\evil.test",
    null,
  ])
    expect(safeRedirect(target)).toBe("/");
});

it("rejects URL control-character redirects", () => {
  for (const path of [
    "/\t/evil.example",
    "/\n/evil.example",
    "/\r/evil.example",
  ])
    expect(safeRedirect(path)).toBe("/");
});
