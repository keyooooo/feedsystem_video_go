import { useState } from "react";
import { postJson } from "../api/client";

export function FoldedComment({ commentId }: { commentId: number }) {
  const [content, setContent] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");

  async function reveal() {
    setBusy(true);
    setMessage("");
    try {
      const result = await postJson<{ content: string }>("/comment/reveal", { comment_id: commentId });
      setContent(result.content);
    } catch {
      setMessage("暂时无法展开此评论");
    } finally {
      setBusy(false);
    }
  }

  return <div>
    <p>{content ?? "评论已折叠"}</p>
    <button type="button" aria-expanded={content !== undefined} disabled={busy}
      onClick={() => { if (content === undefined) void reveal(); else setContent(undefined); }}>
      {content === undefined ? "展开评论" : "收起评论"}
    </button>
    {message && <span role="status">{message}</span>}
  </div>;
}
