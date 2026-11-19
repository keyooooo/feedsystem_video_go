import { it, expect } from "vitest";
import { pagingInput } from "./VideoPages";
it("preserves snapshot and compound cursors without coercing them into offsets", () => {
  expect(pagingInput("hot", { as_of: 9, next_offset: 20 }, false)).toEqual({
    limit: 10,
    as_of: 9,
    offset: 20,
  });
  expect(
    pagingInput(
      "likes",
      { next_likes_count_before: 0, next_id_before: 18 },
      false,
    ),
  ).toEqual({ limit: 10, likes_count_before: 0, id_before: 18 });
  expect(pagingInput("recommend", { next_time: 99 }, true)).toEqual({
    limit: 10,
    latest_time: 0,
  });
});
