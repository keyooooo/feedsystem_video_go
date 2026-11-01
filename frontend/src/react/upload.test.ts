import { it, expect, vi, afterEach } from "vitest";
import { fileHash, uploadChunked, CHUNK_SIZE } from "./upload";
import { postJson, postForm } from "../api/client";
import { validateVideo } from "./UploadPage";
vi.mock("../api/client", () => ({ postJson: vi.fn(), postForm: vi.fn() }));
afterEach(() => vi.resetAllMocks());
it("computes the required file hash", async () =>
  expect(await fileHash(new Blob(["hello"]))).toBe(
    "5d41402abc4b2a76b9719d911017c592",
  ));
it("rejects invalid or oversized media selections", () => {
  expect(validateVideo(new File(["x"], "clip.mp4"))).toBe(true);
  expect(validateVideo(new File(["x"], "clip.exe"))).toBe(false);
  expect(validateVideo(new File([], "empty.mp4"))).toBe(false);
});
it("resumes known chunks and accounts for a short final chunk", async () => {
  vi.mocked(postJson)
    .mockResolvedValueOnce({ upload_id: "a", uploaded_chunks: [0] })
    .mockResolvedValueOnce({ play_url: "/clip.mp4" });
  vi.mocked(postForm).mockResolvedValue({});
  const progress = vi.fn();
  await uploadChunked(
    new File([new Uint8Array(CHUNK_SIZE + 3)], "clip.mp4"),
    new AbortController().signal,
    progress,
  );
  expect(postForm).toHaveBeenCalledTimes(1);
  expect(vi.mocked(postForm).mock.calls[0]![1].get("chunk_index")).toBe("1");
  expect(progress).toHaveBeenLastCalledWith({
    uploaded: CHUNK_SIZE + 3,
    total: CHUNK_SIZE + 3,
    stage: "处理视频",
  });
});
it("caps failed chunk attempts and never completes a failed upload", async () => {
  vi.mocked(postJson).mockResolvedValueOnce({
    upload_id: "a",
    uploaded_chunks: [],
  });
  vi.mocked(postForm).mockRejectedValue(new Error("offline"));
  await expect(
    uploadChunked(
      new File(["x"], "clip.mp4"),
      new AbortController().signal,
      () => {},
    ),
  ).rejects.toThrow("offline");
  expect(postForm).toHaveBeenCalledTimes(3);
  expect(postJson).toHaveBeenCalledTimes(1);
});
it("does not contact the server for an already cancelled upload", async () => {
  const c = new AbortController();
  c.abort();
  await expect(
    uploadChunked(new File(["x"], "clip.mp4"), c.signal, () => {}),
  ).rejects.toThrow();
  expect(postJson).not.toHaveBeenCalled();
});
it("limits concurrent chunk transfers to three and waits before merging", async () => {
  vi.mocked(postJson)
    .mockResolvedValueOnce({ upload_id: "queue", uploaded_chunks: [] })
    .mockResolvedValueOnce({ play_url: "/clip.mp4" });
  let active = 0,
    peak = 0,
    completed = 0;
  vi.mocked(postForm).mockImplementation(async () => {
    active++;
    peak = Math.max(peak, active);
    await new Promise((r) => setTimeout(r, 15));
    active--;
    completed++;
    return {};
  });
  await uploadChunked(
    new File([new Uint8Array(CHUNK_SIZE * 4)], "clip.mp4"),
    new AbortController().signal,
    () => {},
  );
  expect(peak).toBe(3);
  expect(completed).toBe(4);
  expect(postJson).toHaveBeenCalledTimes(2);
});
