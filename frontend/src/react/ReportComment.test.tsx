// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, test, vi } from "vitest";
import { ReportComment } from "./ReportComment";
import { postJson } from "../api/client";

vi.mock("../api/client", () => ({ postJson: vi.fn() }));
beforeEach(() => vi.clearAllMocks());

test("report opens a dialog and retains one request key when retrying", async () => {
  vi.mocked(postJson).mockRejectedValueOnce(new Error("network")).mockResolvedValueOnce({ report_id: 12 });
  render(<MemoryRouter><ReportComment commentId={7} /></MemoryRouter>);
  fireEvent.click(screen.getByRole("button", { name: "举报" }));
  expect(screen.getByRole("dialog", { name: "举报评论" })).toBeTruthy();
  fireEvent.change(screen.getByLabelText("举报原因"), { target: { value: "harassment" } });
  fireEvent.change(screen.getByLabelText("补充说明（选填）"), { target: { value: "针对他人辱骂" } });
  fireEvent.click(screen.getByRole("button", { name: "提交举报" }));
  await screen.findByText("提交失败，请重试");
  fireEvent.click(screen.getByRole("button", { name: "提交举报" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(postJson).toHaveBeenCalledTimes(2);
  expect(vi.mocked(postJson).mock.calls[0][1]).toEqual(vi.mocked(postJson).mock.calls[1][1]);
  expect(screen.getByRole("link", { name: "查看举报" }).getAttribute("href")).toBe("/reports/12");
});

test("cancel closes the report without submitting", () => {
  render(<MemoryRouter><ReportComment commentId={7} /></MemoryRouter>);
  fireEvent.click(screen.getByRole("button", { name: "举报" }));
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(postJson).not.toHaveBeenCalled();
});
