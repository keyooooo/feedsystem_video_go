import { useEffect, useState } from "react";
import { postJson } from "../api/client";

// Mounted only after the user opens the shared folded-comments section.
export function FoldedComment({ commentId }: { commentId: number }) {
  const [content, setContent] = useState<string>();
  const [message, setMessage] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    setContent(undefined);
    setMessage("");
    void postJson<{ content: string }>("/comment/reveal", {
      comment_id: commentId,
    })
      .then((result) => {
        if (active) setContent(result.content);
      })
      .catch(() => {
        if (active) setMessage("暂时无法展开此评论");
      });
    return () => {
      active = false;
    };
  }, [commentId, attempt]);

  if (message)
    return (
      <div role="status">
        {message}{" "}
        <button type="button" onClick={() => setAttempt((value) => value + 1)}>
          重试展开
        </button>
      </div>
    );
  if (content === undefined) return <p role="status">正在展开评论…</p>;
  return <p>{content}</p>;
}
