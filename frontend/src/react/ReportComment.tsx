import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import s from "./CommentModeration.module.css";
import { MoreHorizontal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { postJson } from "../api/client";

export function ReportComment({ commentId }: { commentId: number }) {
  const [open, setOpen] = useState(false);
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      if (event.target instanceof Node && !menu.current?.contains(event.target) && menu.current) {
        menu.current.open = false;
      }
    };
    document.addEventListener("pointerdown", closeOutside);
    return () => document.removeEventListener("pointerdown", closeOutside);
  }, []);
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
    <details ref={menu} className={s.menu} onKeyDown={event => {
      if (event.key === "Escape" && menu.current?.open) {
        event.preventDefault();
        event.stopPropagation();
        menu.current.open = false;
        menu.current.querySelector("summary")?.focus();
      }
    }}>
      <summary aria-label="评论更多操作" title="更多操作"><MoreHorizontal size={20} aria-hidden="true" /></summary>
      <div className={s.menuPanel}>
        {reportId ? <Link to={`/reports/${reportId}`}>查看举报</Link> :
          <button type="button" onClick={() => {
            if (menu.current) menu.current.open = false;
            setContainer(menu.current?.closest<HTMLElement>("[data-inline-comments], [role=dialog]") ?? null);
            setKey(crypto.randomUUID()); setOpen(true);
          }}>举报评论</button>}
      </div>
    </details>
    <Dialog.Root open={open} onOpenChange={value => { if (!busy) setOpen(value); }}>
      <Dialog.Portal container={container}>
        <Dialog.Overlay className={s.reportOverlay} />
        <Dialog.Content className={s.reportDialog} aria-describedby={undefined} onCloseAutoFocus={event => {
          event.preventDefault();
          menu.current?.querySelector("summary")?.focus();
        }}>
          <div className={s.reportHeader}>
            <Dialog.Title>举报评论</Dialog.Title>
            <Dialog.Close asChild><button type="button" disabled={busy} aria-label="关闭举报"><X size={20} /></button></Dialog.Close>
          </div>
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
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
    {!open && message && <span role="status" className={s.status}>{message}</span>}
  </>;
}
