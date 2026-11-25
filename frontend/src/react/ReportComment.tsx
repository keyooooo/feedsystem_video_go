import { Modal } from "./ui";
import s from "./CommentModeration.module.css";
import app from "./App.module.css";
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
  return <>
    {reportId ? <Link className={app.commentAction} to={`/reports/${reportId}`}>查看举报</Link> :
      <button className={app.commentAction} type="button" onClick={() => { setKey(crypto.randomUUID()); setOpen(true); }}>举报</button>}
    <Modal title="举报评论" open={open} onClose={() => { if (!busy) setOpen(false); }}>
      <form className={s.form} onSubmit={e => { e.preventDefault(); void submit(); }}>
        <label>举报原因
          <select value={reason} disabled={busy} onChange={e => { setReason(e.target.value); setKey(crypto.randomUUID()); }}>
            <option value="spam_promo">垃圾营销</option><option value="harassment">定向辱骂</option><option value="media_only">视频画面或音频</option><option value="other">其他原因</option>
          </select>
        </label>
        <label>补充说明（选填）
          <textarea maxLength={512} disabled={busy} value={detail} onChange={e => { setDetail(e.target.value); setKey(crypto.randomUUID()); }} />
        </label>
        <div className={s.actions}>
          <button type="button" disabled={busy} onClick={() => setOpen(false)}>取消</button>
          <button type="submit" disabled={busy}>{busy ? "提交中…" : "提交举报"}</button>
        </div>
        {message && <span role="status" className={s.status}>{message}</span>}
      </form>
    </Modal>
    {!open && message && <span role="status" className={s.status}>{message}</span>}
  </>;
}
