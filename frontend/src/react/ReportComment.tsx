import { useState } from "react";
import { Link } from "react-router-dom";
import { postJson } from "../api/client";

export function ReportComment({ commentId }: { commentId: number }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("spam_promo");
  const [detail, setDetail] = useState("");
  const [key, setKey] = useState("");
  const [reportId, setReportId] = useState<number>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function submit() {
    setBusy(true);
    try {
      const result = await postJson<{ report_id: number }>("/report/create", {
        comment_id: commentId, request_id: key, reason_code: reason, detail: detail.trim(),
      }, { authRequired: true });
      setReportId(result.report_id);
      setOpen(false);
      setMessage("举报已受理");
    } catch { setMessage("提交失败，请重试"); }
    finally { setBusy(false); }
  }
  return <div>
    {reportId ? <Link to={`/reports/${reportId}`}>查看举报 #{reportId}</Link> :
      open ? <>
        <select aria-label="举报原因" value={reason} disabled={busy} onChange={e => { setReason(e.target.value); setKey(crypto.randomUUID()); }}>
          <option value="spam_promo">垃圾营销</option><option value="harassment">定向辱骂</option><option value="media_only">视频画面或音频</option><option value="other">其他原因</option>
        </select>
        <label>补充说明（选填） <textarea maxLength={512} disabled={busy} value={detail}
          onChange={e => { setDetail(e.target.value); setKey(crypto.randomUUID()); }} /></label>
        <button type="button" disabled={busy} onClick={() => void submit()}>提交举报</button>
        <button type="button" disabled={busy} onClick={() => setOpen(false)}>取消</button>
      </> : <button type="button" onClick={() => { setKey(crypto.randomUUID()); setOpen(true); }}>举报</button>}
    <span role="status">{message}</span>
  </div>;
}
