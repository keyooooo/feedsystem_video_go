import { useState } from "react";
import { postJson } from "../api/client";
import s from "./ModerationPage.module.css";

type Evidence = {
  id: string;
  source: string;
  status: "ok" | "empty" | "unavailable";
  text?: string;
};

export type ShadowRun = {
  id: number;
  attempt: number;
  model?: string;
  policy_version?: string;
  prompt_version?: string;
  rule_version?: string;
  fold_candidate: boolean;
  execution_outcome?: string;
  failure?: string;
  duration_ms: number;
  evidence?: { items?: Evidence[]; tool_trace?: {
    round: number; name: string; status: "executed" | "blocked" | "failed"; evidence_ids?: string[];
  }[] };
  suggestion?: {
    action?: string;
    policy_id?: string;
    confidence?: number;
    evidence_ids?: string[];
    reason?: string;
  };
};

const sources: Record<string, string> = {
  target: "目标评论", report: "举报说明", video: "视频文字资料",
  author: "作者简介", parent: "被回复评论", thread: "补充线程证据", history: "历史终审",
};
const statuses = { ok: "已取得", empty: "无内容", unavailable: "暂不可用" };
const toolNames: Record<string, string> = { get_thread_context: "查询线程上下文", get_policy_clause: "查询政策条款", unknown: "未注册工具", batch: "补证请求" };
const toolStatuses = { executed: "已执行", blocked: "已拦截", failed: "执行失败" };
const actions: Record<string, string> = {
  suggest_keep: "建议维持", suggest_fold: "建议折叠", needs_review: "建议人工复核",
};
const policies: Record<string, string> = { SPAM_PROMO: "垃圾营销", HARASSMENT: "定向辱骂" };
const failures: Record<string, string> = {
  insufficient_evidence: "必要证据缺失", invalid_output: "建议或证据引用校验失败",
  invalid_tool_call: "补证请求超出允许范围", tool_budget_exhausted: "达到补证调用上限",
  model_unavailable: "模型暂不可用", tool_unavailable: "补证暂不可用",
  deadline_exceeded: "审核超时", lease_expired: "执行中断，租约已过期",
  target_removed: "作者已删除评论", target_unavailable: "无法读取评论",
};

const outcomes: Record<string, string> = {
  applied: "已自动折叠并结案",
  target_removed: "评论已删除，工单已结案",
  retry_scheduled: "等待重试",
  evaluation_failed: "审核执行异常，已转人工",
  not_eligible: "未达到自动折叠条件，已转人工",
  invalid_terminal_intent: "决策意图校验未通过，已转人工",
  rollout_missing: "自动折叠未启用，已转人工",
  rollout_ineligible: "当前工单未满足放量条件，已转人工",
  calibration_pending: "独立复核未达门槛，已转人工",
  human_override: "保留人工决定，已转人工",
  target_changed: "目标评论已变更，已转人工",
  target_evidence_stale: "目标证据已失效，已转人工",
  parent_unavailable: "回复语境不可用，已转人工",
  parent_changed: "回复语境已变更，已转人工",
  target_version_changed: "评论治理状态已变更，已转人工",
  prior_decision_missing: "无法关联既有处置，已转人工",
  prior_decision_mismatch: "既有处置不匹配，已转人工",
};

export function ReviewSuggestions({ caseId }: { caseId: number }) {
  const [runs, setRuns] = useState<ShadowRun[]>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);

  async function reveal() {
    setBusy(true);
    setError(false);
    try {
      const result = await postJson<{ shadow_runs: ShadowRun[] }>(
        "/moderation/suggestions", { case_id: caseId }, { authRequired: true },
      );
      setRuns(result.shadow_runs);
    } catch { setError(true); }
    finally { setBusy(false); }
  }

  if (runs) return <ModerationEvidence runs={runs} />;
  return <div className={s.evidence}>
    <button type="button" disabled={busy} onClick={() => void reveal()}>查看模型建议</button>
    {error && <p role="alert">建议加载失败，请重试</p>}
  </div>;
}

function ModerationEvidence({ runs }: { runs: ShadowRun[] }) {
  if (runs.length === 0) return null;
  return <section aria-label="影子审核记录" className={s.evidence}>
    <h3>影子审核记录</h3>
    {[...runs].reverse().map(run => {
      const suggestion = run.suggestion;
      const references = new Set(suggestion?.evidence_ids);
      const policy = suggestion?.policy_id;
      const outcome = run.execution_outcome && outcomes[run.execution_outcome];
      return <details key={run.id}>
        <summary>第 {run.attempt} 次审核 · {run.failure ? "转人工" : "查看建议与证据"}</summary>
        {outcome && <p><strong>{outcome}</strong></p>}
        {run.failure && <p>{failures[run.failure] ?? run.failure}</p>}
        {suggestion?.action && <p><strong>{actions[suggestion.action] ?? suggestion.action}</strong>
          {policy && <> · {policies[policy] ?? policy}</>}
          {typeof suggestion.confidence === "number" && <> · 置信度 {Math.round(suggestion.confidence * 100)}%</>}
        </p>}
        {suggestion?.reason && <p>{suggestion.reason}</p>}
        {run.fold_candidate && <p>符合自动折叠规则</p>}
        <ul>
          {run.evidence?.items?.map(item => <li key={item.id}>
            <strong>{sources[item.source] ?? item.source}</strong> · {statuses[item.status]}
            {references.has(item.id) && <span> · 建议引用</span>}
            {item.text && <blockquote>{item.text}</blockquote>}
          </li>)}
        </ul>
        {!!run.evidence?.tool_trace?.length && <>
          <h4>补证轨迹</h4>
          <ol>{run.evidence.tool_trace.map((step, index) => <li key={index}>
            第 {step.round} 轮 · {toolNames[step.name] ?? step.name} · {toolStatuses[step.status]}
            {step.evidence_ids?.length ? <> · {step.evidence_ids.join("、")}</> : null}
          </li>)}</ol>
        </>}
        <small>{[run.model, run.policy_version, run.prompt_version, run.rule_version, `${run.duration_ms} ms`].filter(Boolean).join(" · ")}</small>
      </details>;
    })}
  </section>;
}
