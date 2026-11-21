import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import * as comments from "../api/comment";
import type {
  Comment,
  CommentReply,
  CommentThread,
  FeedVideoItem,
} from "../api/types";
import {
  Empty,
  ErrorBox,
  Modal,
  useIdentity,
  useMedia,
  useSession,
} from "./ui";
import s from "./App.module.css";

type Replies = {
  items: CommentReply[];
  cursor?: number;
  expanded: boolean;
  loading?: boolean;
  error?: unknown;
};

type ReplyTarget = { rootId: number; id: number; username: string };
type DeleteTarget = { rootId: number; id: number };

function mergeComments<T extends Comment>(
  current: T[],
  incoming: T[],
  descending = false,
) {
  const byId = new Map(current.map((item) => [item.id, item]));
  for (const item of incoming) byId.set(item.id, item);
  return [...byId.values()].sort((left, right) =>
    descending ? right.id - left.id : left.id - right.id,
  );
}

export function CommentPanel({
  item,
  onClose,
}: {
  item: FeedVideoItem | null;
  onClose: () => void;
}) {
  const session = useSession();
  return (
    item && (
      <OpenComments
        key={`${item.id}:${session.revision}`}
        videoId={item.id}
        onClose={onClose}
      />
    )
  );
}

function OpenComments({
  videoId,
  onClose,
}: {
  videoId: number;
  onClose: () => void;
}) {
  const wide = useMedia("(min-width: 1200px)");
  const session = useSession();
  const identity = useIdentity();
  const location = useLocation();
  const navigate = useNavigate();
  const input = useRef<HTMLTextAreaElement>(null);
  const replyRequests = useRef(new Set<number>());
  const threadRequest = useRef(0);
  const lastThreadLoad = useRef({ after: 0, preserve: false });
  const deletedReplies = useRef(new Set<number>());
  const deletedRoots = useRef(new Map<number, string>());
  const [threads, setThreads] = useState<CommentThread[]>([]);
  const [cursor, setCursor] = useState<number>();
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<unknown>(null);
  const [replies, setReplies] = useState<Record<number, Replies>>({});
  const [target, setTarget] = useState<ReplyTarget | null>(null);
  const [deleting, setDeleting] = useState<DeleteTarget | null>(null);
  const [content, setContent] = useState("");
  const [sending, setSending] = useState(false);
  const [failure, setFailure] = useState<unknown>(null);

  const loadThreads = useCallback(
    async (after = 0, preserve = false) => {
      const request = ++threadRequest.current;
      lastThreadLoad.current = { after, preserve };
      setLoading(true);
      setListError(null);
      try {
        const page = await comments.listThreads(videoId, after);
        if (request !== threadRequest.current) return;
        setThreads((old) =>
          mergeComments(
            after || preserve ? old : [],
            page.comments,
            true,
          ).flatMap((thread) => {
            const removedAt = deletedRoots.current.get(thread.id);
            if (!removedAt) return [thread];
            return thread.reply_count
              ? [{ ...thread, removed_at: removedAt, content: "" }]
              : [];
          }),
        );
        setCursor((old) =>
          preserve && old !== undefined ? old : page.next_cursor,
        );
      } catch (error) {
        if (request === threadRequest.current) setListError(error);
      } finally {
        if (request === threadRequest.current) setLoading(false);
      }
    },
    [videoId],
  );

  useEffect(() => {
    void loadThreads();
    return () => {
      threadRequest.current++;
    };
  }, [loadThreads]);

  function updateReplies(rootId: number, update: (old: Replies) => Replies) {
    setReplies((old) => ({
      ...old,
      [rootId]: update(old[rootId] ?? { items: [], expanded: false }),
    }));
  }

  async function loadReplies(rootId: number, after = 0) {
    if (replyRequests.current.has(rootId)) return;
    replyRequests.current.add(rootId);
    updateReplies(rootId, (old) => ({
      ...old,
      expanded: true,
      loading: true,
      error: null,
    }));
    try {
      const page = await comments.listReplies(videoId, rootId, after);
      updateReplies(rootId, (old) => ({
        ...old,
        items: mergeComments(old.items, page.comments).filter(
          (row) => !deletedReplies.current.has(row.id),
        ),
        cursor: page.next_cursor,
        loading: false,
      }));
    } catch (error) {
      updateReplies(rootId, (old) => ({ ...old, loading: false, error }));
    } finally {
      replyRequests.current.delete(rootId);
    }
  }

  function selectReply(rootId: number, row: Comment) {
    setTarget({ rootId, id: row.id, username: row.username });
    setFailure(null);
    input.current?.focus();
  }

  async function send() {
    if (sending || !content.trim()) return;
    if (!session.token) {
      navigate(
        "/account?redirect=" +
          encodeURIComponent(location.pathname + location.search),
      );
      return;
    }
    setSending(true);
    setFailure(null);
    try {
      const response = await comments.publish(
        videoId,
        content.trim(),
        target?.id,
      );
      if (target) {
        const reply = response.comment;
        if (!reply) throw new Error("未能确认回复，请刷新评论后重试");
        setThreads((old) =>
          old.map((row) =>
            row.id === target.rootId
              ? { ...row, reply_count: row.reply_count + 1 }
              : row,
          ),
        );
        updateReplies(target.rootId, (old) => ({
          ...old,
          expanded: true,
          items: mergeComments(old.items, [reply]),
        }));
        if (replies[target.rootId]?.cursor === undefined)
          void loadReplies(target.rootId);
      } else {
        void loadThreads(0, true);
      }
      setContent("");
      setTarget(null);
    } catch (error) {
      setFailure(error);
    } finally {
      setSending(false);
    }
  }

  async function remove() {
    if (sending || !deleting) return;
    setSending(true);
    setFailure(null);
    try {
      await comments.remove(deleting.id);
      if (deleting.id === deleting.rootId) {
        const removedAt = new Date().toISOString();
        deletedRoots.current.set(deleting.id, removedAt);
        setThreads((old) =>
          old.flatMap((row) =>
            row.id !== deleting.id
              ? [row]
              : row.reply_count
                ? [{ ...row, removed_at: removedAt, content: "" }]
                : [],
          ),
        );
      } else {
        deletedReplies.current.add(deleting.id);
        updateReplies(deleting.rootId, (old) => ({
          ...old,
          items: old.items.filter((row) => row.id !== deleting.id),
        }));
        setThreads((old) =>
          old.flatMap((row) => {
            if (row.id !== deleting.rootId) return [row];
            const replyCount = Math.max(0, row.reply_count - 1);
            return row.removed_at && !replyCount
              ? []
              : [{ ...row, reply_count: replyCount }];
          }),
        );
      }
      if (
        target &&
        (target.id === deleting.id || target.rootId === deleting.id)
      )
        setTarget(null);
      setDeleting(null);
    } catch (error) {
      setFailure(error);
    } finally {
      setSending(false);
    }
  }

  function renderActions(root: CommentThread, row: Comment) {
    return (
      <div className={s.commentActions}>
        {!root.removed_at && (
          <button
            type="button"
            disabled={sending}
            onClick={() => selectReply(root.id, row)}
          >
            回复
          </button>
        )}
        {identity?.account_id === row.author_id && (
          <button
            type="button"
            disabled={sending}
            onClick={() => {
              setFailure(null);
              setDeleting({ id: row.id, rootId: root.id });
            }}
          >
            删除
          </button>
        )}
      </div>
    );
  }

  return (
    <>
      <Modal title="评论" inline={wide} open onClose={onClose}>
        <div className={s.dialogBody}>
          {loading && !threads.length && (
            <div
              className={s.skeleton}
              role="status"
              aria-label="正在加载评论"
            />
          )}
          {!loading && !threads.length && !listError && (
            <Empty title="还没有评论">
              <p>来聊聊你的想法吧。</p>
            </Empty>
          )}
          {threads.map((root) => {
            const thread = replies[root.id];
            return (
              <section key={root.id} className={s.commentThread}>
                <article className={s.comment}>
                  {root.removed_at ? (
                    <p>该评论已删除</p>
                  ) : (
                    <>
                      <strong>{root.username}</strong>
                      <p>{root.content}</p>
                      <small>
                        {new Date(root.created_at).toLocaleString()}
                      </small>
                      {renderActions(root, root)}
                    </>
                  )}
                  {root.reply_count > 0 && (
                    <button
                      type="button"
                      aria-expanded={!!thread?.expanded}
                      onClick={() => {
                        if (thread?.expanded || thread?.cursor !== undefined) {
                          updateReplies(root.id, (old) => ({
                            ...old,
                            expanded: !old.expanded,
                          }));
                        } else void loadReplies(root.id);
                      }}
                    >
                      {thread?.expanded
                        ? "收起回复"
                        : `展开 ${root.reply_count} 条回复`}
                    </button>
                  )}
                </article>
                {thread?.expanded && root.reply_count > 0 && (
                  <div className={s.commentReplies}>
                    {thread.items.map((reply) => (
                      <article key={reply.id} className={s.comment}>
                        <strong>{reply.username}</strong>
                        <p>
                          {reply.reply_target_deleted ||
                          deletedReplies.current.has(
                            reply.reply_to_comment_id,
                          ) ||
                          deletedRoots.current.has(
                            reply.reply_to_comment_id,
                          ) ? (
                            <span className={s.muted}>回复已删除的评论 · </span>
                          ) : (
                            reply.reply_to_username && (
                              <span className={s.muted}>
                                回复 @{reply.reply_to_username} ·{" "}
                              </span>
                            )
                          )}
                          {reply.content}
                        </p>
                        <small>
                          {new Date(reply.created_at).toLocaleString()}
                        </small>
                        {renderActions(root, reply)}
                      </article>
                    ))}
                    {thread.loading && <p role="status">正在加载回复…</p>}
                    <ErrorBox
                      error={thread.error}
                      retry={() => void loadReplies(root.id, thread.cursor)}
                    />
                    {!!thread.cursor && !thread.error && (
                      <button
                        type="button"
                        disabled={thread.loading}
                        onClick={() => void loadReplies(root.id, thread.cursor)}
                      >
                        加载更多回复
                      </button>
                    )}
                  </div>
                )}
              </section>
            );
          })}
          <ErrorBox
            error={listError}
            retry={() => {
              const { after, preserve } = lastThreadLoad.current;
              void loadThreads(after, preserve);
            }}
          />
          {!!cursor && !listError && (
            <button
              type="button"
              disabled={loading}
              onClick={() => void loadThreads(cursor)}
            >
              加载更多评论
            </button>
          )}
        </div>
        {!deleting && <ErrorBox error={failure} />}
        {target && (
          <div className={s.commentActions}>
            <span>回复 @{target.username}</span>
            <button
              type="button"
              disabled={sending}
              onClick={() => {
                setTarget(null);
                setFailure(null);
              }}
            >
              取消回复
            </button>
          </div>
        )}
        <label>
          你的评论
          <textarea
            ref={input}
            value={content}
            disabled={sending}
            onChange={(event) => setContent(event.target.value)}
            placeholder={
              target ? `回复 @${target.username}` : "友善表达，分享想法"
            }
          />
        </label>
        <button
          className={s.primary}
          disabled={sending || !content.trim()}
          onClick={() => void send()}
        >
          {sending ? "发送中…" : target ? "发送回复" : "发送评论"}
        </button>
      </Modal>
      <Modal
        title="删除这条评论？"
        open={deleting !== null}
        onClose={() => {
          if (!sending) {
            setDeleting(null);
            setFailure(null);
          }
        }}
      >
        <ErrorBox error={failure} />
        <button disabled={sending} onClick={() => void remove()}>
          确认删除
        </button>
      </Modal>
    </>
  );
}
