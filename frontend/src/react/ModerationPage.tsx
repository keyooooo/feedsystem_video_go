import { useCallback, useEffect, useState } from "react";
import { ApiError, openModerationStream, postJson } from "../api/client";
import { useNotificationStream } from "./useNotificationStream";
import { useSession } from "./ui";
import { ReviewSuggestions } from "./ModerationEvidence";
import s from "./ModerationPage.module.css";

type Case = { id: number; version: number; state: string; routing_reason?: string };
type Action = "keep" | "fold" | "restore";
type Detail = {
  case: Case;
  target_version: number;
  reason_code: string;
  detail: string;
  comment: { content: string; removed_at?: string; moderation_folded?: boolean };
  shadow_run_count?: number;
  reply_context?: string;
  decisions: { id: number; reviewer_id: number; action: Action | "target_removed"; reason: string; false_fold?: boolean }[];
};

type ReviewVersion = { case_version: number; target_version: number };
type ReviewSubmission = ReviewVersion & {
  case_id: number; action: Action; reason: string; request_id: string;
  false_fold: boolean; policy_id: string;
};

const reviewVersion = (detail: Detail): ReviewVersion => ({
  case_version: detail.case.version,
  target_version: detail.target_version,
});

const auth = { authRequired: true };
const actionNames = { keep: "维持现状", fold: "折叠评论", restore: "恢复展示" };
const stateNames: Record<string, string> = { queued: "待处理", awaiting_review: "待终审", running: "审核中", resolved: "已处理" };
const reasonNames: Record<string, string> = { spam_promo: "垃圾营销", harassment: "定向辱骂", media_only: "视频画面或音频", other: "其他原因" };

export default function ModerationPage() {
  const { token } = useSession();
  const [cases, setCases] = useState<Case[]>();
  const [active, setActive] = useState(0);
  const [filter, setFilter] = useState("awaiting_review");
  const [after, setAfter] = useState(0);
  const [revision, setRevision] = useState(0);
  const [detail, setDetail] = useState<Detail>();
  const [reason, setReason] = useState("");
  const [falseFold, setFalseFold] = useState(false);
  const [policyId, setPolicyID] = useState("");
  const [action, setAction] = useState<Action>("keep");
  const [draftVersion, setDraftVersion] = useState<ReviewVersion>();
  const [submission, setSubmission] = useState<ReviewSubmission>();
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [access, setAccess] = useState(false);
  const refresh = useCallback(() => setRevision(value => value + 1), []);
  useNotificationStream(access ? (token ?? "") : "", refresh, openModerationStream);

  useEffect(() => {
    let disposed = false;
    void postJson<{ cases: Case[] }>("/moderation/list", { state: filter, after }, auth)
      .then(result => { if (!disposed) { setCases(result.cases); setAccess(true); } })
      .catch((err: unknown) => {
        if (disposed) return;
        if (err instanceof ApiError && [401, 403].includes(err.status)) {
          setAccess(false);
          setCases(undefined);
          setActive(0);
          setDetail(undefined);
          setError("当前账号无法访问审核台");
        } else setError("工单加载失败，请刷新");
      });
    return () => { disposed = true; };
  }, [filter, after, revision]);

  useEffect(() => {
    if (!active) return;
    let disposed = false;
    void postJson<Detail>("/moderation/detail", { case_id: active }, auth)
      .then(result => { if (!disposed) setDetail(result); })
      .catch(() => { if (!disposed) setError("详情加载失败，请重试"); });
    return () => { disposed = true; };
  }, [active, revision]);

  const latestDecision = detail?.decisions.at(-1);
  const mayConfirmFalseFold = latestDecision?.action === "fold" && latestDecision.reviewer_id === 0;

  const stale = conflict || !!(draftVersion && detail && (
    draftVersion.case_version !== detail.case.version || draftVersion.target_version !== detail.target_version
  ));

  function markDraft() {
    if (!draftVersion && detail) setDraftVersion(reviewVersion(detail));
    setSubmission(undefined);
  }

  async function save() {
    if (!detail || busy || (stale && !submission)) return;
    const payload = submission ?? {
      ...reviewVersion(detail),
      case_id: detail.case.id,
      action,
      false_fold: action === "restore" && mayConfirmFalseFold && falseFold,
      policy_id: action === "fold" ? policyId : "",
      reason: reason.trim(),
      request_id: crypto.randomUUID(),
    };
    setSubmission(payload);
    setBusy(true);
    setError("");
    try {
      await postJson("/moderation/decide", payload, auth);
      setReason("");
      setSubmission(undefined);
      setDraftVersion(undefined);
      setConflict(false);
      refresh();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSubmission(undefined);
        setConflict(true);
        refresh();
      } else setError("未确认保存结果，请重试同一决定");
    } finally { setBusy(false); }
  }

  return <main className={s.page}>
    <header className={s.header}><h1>评论审核</h1><button type="button" onClick={refresh}>刷新</button></header>
    {error && <p role="alert">{error}</p>}
    {access && <div className={s.layout}>
      <section className={s.queue} aria-label="工单队列">
        <label>工单状态 <select value={filter} onChange={e => { setFilter(e.target.value); setAfter(0); }}>
          <option value="queued">待处理</option><option value="awaiting_review">待终审</option><option value="resolved">已处理</option>
        </select></label>
        {cases?.length === 0 && <p>当前没有工单</p>}
        <ul>{cases?.map(item => <li key={item.id}><button type="button" disabled={busy} aria-pressed={active === item.id}
          onClick={() => { if (active === item.id) return; setDetail(undefined); setActive(item.id); setReason(""); setSubmission(undefined); setDraftVersion(undefined); setConflict(false); setAction("keep"); setPolicyID(""); setFalseFold(false); setError(""); }}>
          #{item.id} <span>{stateNames[item.state] ?? item.state}</span>
        </button></li>)}</ul>
        {after > 0 && <button type="button" onClick={() => setAfter(0)}>返回首页</button>}
        {cases?.length === 50 && <button type="button" onClick={() => setAfter(cases[49].id)}>下一页</button>}
      </section>
      <section className={s.detail} aria-label="工单详情">
        {detail ? <>
          <h2>工单 #{detail.case.id} <small>{stateNames[detail.case.state]}</small></h2>
          <p>{reasonNames[detail.reason_code] ?? detail.reason_code} {detail.detail}</p>
          {detail.case.routing_reason === "outside_text_scope" && <p>涉及视频画面或音频，需人工核对。</p>}
          <blockquote>{detail.comment.removed_at ? "作者已删除评论" : detail.comment.content}</blockquote>
          {detail.reply_context && <><h3>被回复评论</h3><blockquote>{detail.reply_context}</blockquote></>}
          <form onSubmit={e => { e.preventDefault(); void save(); }}>
            <label>处置方式 <select disabled={busy} value={action} onChange={e => { setAction(e.target.value as Action); setFalseFold(false); markDraft(); }}>
              <option value="keep">{actionNames.keep}</option>
              <option value="fold" disabled={!!detail.comment.removed_at}>{actionNames.fold}</option>
              <option value="restore" disabled={!detail.comment.moderation_folded || !!detail.comment.removed_at}>{actionNames.restore}</option>
            </select></label>
            {action === "fold" && <label>适用条款 <select disabled={busy} value={policyId} onChange={e => { setPolicyID(e.target.value); markDraft(); }}>
              <option value="">未指定</option><option value="SPAM_PROMO">垃圾营销</option><option value="HARASSMENT">定向辱骂</option>
            </select></label>}
            {action === "restore" && mayConfirmFalseFold && <label>
              <input type="checkbox" checked={falseFold} disabled={busy} onChange={e => {
                setFalseFold(e.target.checked); markDraft();
              }} />
              确认自动折叠误判并暂停对应版本
            </label>}
            <label>审核理由 <textarea required maxLength={200} disabled={busy} value={reason} onChange={e => { setReason(e.target.value); markDraft(); }} /></label>
            {stale && !submission && <div role="alert">
              工单或评论已变更，请核对当前版本后再提交。
              <button type="button" disabled={busy} onClick={() => {
                setDraftVersion(reviewVersion(detail));
                setConflict(false);
                setError("");
              }}>已核对当前版本</button>
            </div>}
            <button type="submit" disabled={busy || !reason.trim() || (stale && !submission) || (!submission && detail.case.state === "running")}>
              {submission && !busy ? "重试保存决定" : "保存决定"}
            </button>
          </form>
          {(detail.shadow_run_count ?? 0) > 0 && <ReviewSuggestions key={`${detail.case.id}:${detail.shadow_run_count}`} caseId={detail.case.id} />}
          {detail.decisions.length > 0 && <><h3>处置记录</h3><ol>{detail.decisions.map(item => <li key={item.id}>{item.action === "target_removed" ? "目标已删除" : actionNames[item.action]} · {item.reason}{item.false_fold && " · 确认误折叠"}</li>)}</ol></>}
        </> : <p>{active ? "正在加载工单详情…" : "选择工单查看举报内容"}</p>}
      </section>
    </div>}
  </main>;
}
