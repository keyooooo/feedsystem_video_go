import { it, expect, vi, beforeEach } from "vitest";
import { authSession } from "../session/auth";
import { loadFollowing, toggleFollowing, useSocialState } from "./social-state";
import * as api from "../api/social";
vi.mock("../api/social", () => ({
  getAllVloggers: vi.fn(),
  follow: vi.fn(),
  unfollow: vi.fn(),
}));
beforeEach(() => {
  vi.resetAllMocks();
  authSession.getState().setTokens("token", "refresh");
  vi.mocked(api.getAllVloggers).mockResolvedValue({ vloggers: [] });
  vi.mocked(api.follow).mockResolvedValue({ message: "ok" });
  vi.mocked(api.unfollow).mockResolvedValue({ message: "ok" });
});
it("retains accepted follow state while the asynchronous backend catches up", async () => {
  await loadFollowing();
  await toggleFollowing(7);
  expect(useSocialState.getState().ids).toContain(7);
  await loadFollowing();
  expect(api.getAllVloggers).toHaveBeenCalledTimes(1);
  expect(useSocialState.getState().ids).toContain(7);
  await toggleFollowing(7);
  expect(useSocialState.getState().ids).not.toContain(7);
});
it("does not mutate follow state on failure or retain it after logout", async () => {
  await loadFollowing();
  vi.mocked(api.follow).mockRejectedValueOnce(new Error("offline"));
  await expect(toggleFollowing(4)).rejects.toThrow();
  expect(useSocialState.getState().ids).toEqual([]);
  await toggleFollowing(4);
  authSession.getState().clearTokens();
  expect(useSocialState.getState().ids).toEqual([]);
});
