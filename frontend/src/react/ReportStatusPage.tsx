import { useParams } from "react-router-dom";
import { ApiError, postJson } from "../api/client";
import { useLoad } from "./ui";
import s from "./ModerationPage.module.css";

type ReportStatus = { report_id: number; state: string; result?: string };

const results: Record<string, string> = {
  keep: "举报已处理，评论维持当前展示状态",
  fold: "举报已处理，评论已折叠",
  restore: "举报已复核，评论已恢复展示",
  target_removed: "举报已处理，作者已删除评论",
};

const pending: Record<string, string> = {
  accepted: "举报已受理",
  queued: "举报正在排队处理",
  running: "举报正在审核",
  awaiting_review: "举报等待人工终审",
};

export default function ReportStatusPage() {
  const { id } = useParams();
  const reportId = Number(id);
  if (!id || !/^\d+$/.test(id) || !Number.isSafeInteger(reportId) || reportId < 1) {
    return <section className={s.page}><h1>举报结果</h1><p>举报编号无效</p></section>;
  }
  return <ReportStatusDetails reportId={reportId} />;
}

function ReportStatusDetails({ reportId }: { reportId: number }) {
  const report = useLoad(String(reportId), () => postJson<ReportStatus>(
    "/report/get", { report_id: reportId }, { authRequired: true },
  ), true);
  const unavailable = report.error instanceof ApiError && report.error.status === 404;
  const status = report.data && (report.data.state === "resolved"
    ? results[report.data.result ?? ""] ?? "举报已处理"
    : pending[report.data.state] ?? "举报待处理");

  return <section className={s.page} aria-labelledby="report-status-title">
    <header className={s.header}>
      <h1 id="report-status-title">举报 #{reportId}</h1>
      {!unavailable && <button type="button" disabled={report.loading} onClick={report.reload}>刷新结果</button>}
    </header>
    {report.error ? <p role="alert">{unavailable ? "找不到这条举报" : "暂时无法查询，请刷新重试"}</p> :
      <p role="status">{status ?? "正在查询举报结果…"}</p>}
  </section>;
}
