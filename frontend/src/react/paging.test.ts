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
      "hot",
      {
        as_of: 0,
        next_offset: 0,
        next_latest_popularity: 0,
        next_latest_before: "2026-11-20T13:32:00Z",
        next_latest_id_before: 18,
      },
      false,
    ),
  ).toEqual({
    limit: 10,
    as_of: 0,
    offset: 0,
    latest_popularity: 0,
    latest_before: "2026-11-20T13:32:00Z",
    latest_id_before: 18,
  });
  expect(pagingInput("following", { next_time: 99 }, true)).toEqual({
    limit: 10,
    latest_time: 0,
  });
});
