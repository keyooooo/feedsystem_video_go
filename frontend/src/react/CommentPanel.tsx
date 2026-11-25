import moderation from "./CommentModeration.module.css";
import { FoldedComment } from "./FoldedComment";
import { ReportComment } from "./ReportComment";
import * as Dialog from "@radix-ui/react-dialog";
import { ChevronDown, ChevronUp, Heart, CornerDownRight } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import * as comments from "../api/comment";
import type {
  Comment,
  CommentLikeState,
  CommentReply,
  CommentThread,
  FeedVideoItem,
} from "../api/types";
import {
  Avatar,
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

const relativeTime = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });

function commentTime(value: string) {
  const date = new Date(value);
  const minutes = Math.max(
    0,
    Math.floor((Date.now() - date.getTime()) / 60000),
  );
  if (minutes < 1) return "刚刚";
  if (minutes < 60) return relativeTime.format(-minutes, "minute");
  if (minutes < 1440)
    return relativeTime.format(-Math.floor(minutes / 60), "hour");
  if (minutes < 10080)
    return relativeTime.format(-Math.floor(minutes / 1440), "day");
  return date.toLocaleDateString("zh-CN");
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
  const pendingLikes = useRef(new Set<number>());
  const [likes, setLikes] = useState<
    Record<number, CommentLikeState & { pending?: boolean }>
  >({});
  const [total, setTotal] = useState<number>();
  const scrollArea = useRef<HTMLDivElement>(null);
  const replyRequests = useRef(new Set<number>());
  const threadRequest = useRef(0);
  const lastThreadLoad = useRef({ after: 0, preserve: false });
  const deletedReplies = useRef(new Set<number>());
  const deletedRoots = useRef(new Set<number>());
  const [threads, setThreads] = useState<CommentThread[]>([]);
  const [cursor, setCursor] = useState<number>();
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<unknown>(null);
  const [replies, setReplies] = useState<Record<number, Replies>>({});
  const [target, setTarget] = useState<ReplyTarget | null>(null);
  const [deleting, setDeleting] = useState<DeleteTarget | null>(null);
  const removeTrigger = useRef<HTMLButtonElement>(null);
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
        if (page.total_count !== undefined) setTotal(page.total_count);
        setThreads((old) =>
          mergeComments(
            after || preserve ? old : [],
            page.comments,
            true,
          ).filter(
            (thread) =>
              !thread.removed_at && !deletedRoots.current.has(thread.id),
          ),
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
      if (deletedRoots.current.has(rootId)) return;
      updateReplies(rootId, (old) => ({
        ...old,
        items: mergeComments(old.items, page.comments).filter(
          (row) => !deletedReplies.current.has(row.id),
        ),
        cursor: page.next_cursor,
        loading: false,
      }));
    } catch (error) {
      if (!deletedRoots.current.has(rootId))
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
        setTotal((old) => (old === undefined ? old : old + 1));
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
      const response = await comments.remove(deleting.id);
      const rootRemoved = deleting.id === deleting.rootId;
      const fallbackCount = rootRemoved
        ? 1 + (threads.find((row) => row.id === deleting.id)?.reply_count ?? 0)
        : 1;
      setTotal((old) =>
        old === undefined
          ? old
          : Math.max(0, old - (response.deleted_count ?? fallbackCount)),
      );
      if (rootRemoved) {
        deletedRoots.current.add(deleting.id);
        setThreads((old) => old.filter((row) => row.id !== deleting.id));
        setReplies((old) => {
          const next = { ...old };
          delete next[deleting.id];
          return next;
        });
      } else {
        deletedReplies.current.add(deleting.id);
        updateReplies(deleting.rootId, (old) => ({
          ...old,
          items: old.items.filter((row) => row.id !== deleting.id),
        }));
        setThreads((old) =>
          old.map((row) =>
            row.id === deleting.rootId
              ? { ...row, reply_count: Math.max(0, row.reply_count - 1) }
              : row,
          ),
        );
      }
      if (
        target &&
        (target.id === deleting.id || target.rootId === deleting.id)
      )
        setTarget(null);
      setDeleting(null);
      if (loading || response.deleted_count === undefined)
        void loadThreads(0, true);
    } catch (error) {
      setFailure(error);
    } finally {
      setSending(false);
    }
  }

  async function toggleLike(row: CommentThread | CommentReply) {
    if (!session.token) {
      navigate(
        "/account?redirect=" +
          encodeURIComponent(location.pathname + location.search),
      );
      return;
    }
    if (pendingLikes.current.has(row.id)) return;
    const previous = likes[row.id] ?? {
      likes_count: row.likes_count ?? 0,
      is_liked: !!row.is_liked,
    };
    const liked = !previous.is_liked;
    pendingLikes.current.add(row.id);
    setFailure(null);
    setLikes((old) => ({
      ...old,
      [row.id]: {
        is_liked: liked,
        likes_count: Math.max(0, previous.likes_count + (liked ? 1 : -1)),
        pending: true,
      },
    }));
    try {
      const confirmed = await comments.setLike(row.id, liked);
      setLikes((old) => ({ ...old, [row.id]: confirmed }));
    } catch (error) {
      setLikes((old) => ({ ...old, [row.id]: previous }));
      setFailure(error);
    } finally {
      pendingLikes.current.delete(row.id);
    }
  }

  function collapse(rootId: number) {
    updateReplies(rootId, (old) => ({ ...old, expanded: false }));
    requestAnimationFrame(() => {
      const root = scrollArea.current?.querySelector<HTMLElement>(
        `[data-comment-root="${rootId}"]`,
      );
      root
        ?.querySelector("button[aria-expanded]")
        ?.scrollIntoView({ block: "nearest" });
    });
  }

  function renderComment(
    root: CommentThread,
    row: CommentThread | CommentReply,
  ) {
    const like = likes[row.id] ?? row;
    const isReply = "reply_to_comment_id" in row;
    const deletedTarget =
      isReply &&
      (row.reply_target_deleted ||
        deletedReplies.current.has(row.reply_to_comment_id));
    const replyName =
      isReply &&
      (row.reply_to_username
        ? `@${row.reply_to_username}${deletedTarget ? "（评论已删除）" : ""}`
        : deletedTarget && "评论已删除");
    return (
      <div className={s.commentRow}>
        <Link
          to={`/u/${row.author_id}`}
          aria-label={`查看 ${row.username} 的主页`}
          className={s.commentAvatar}
        >
          <Avatar name={row.username} url={row.avatar_url} />
        </Link>
        <div className={s.commentCopy}>
          <div className={moderation.commentHeading}>
            <div className={s.commentByline}>
            <Link to={`/u/${row.author_id}`}>
              <strong>{row.username}</strong>
            </Link>
            {replyName && (
              <span className={s.commentTarget}>
                <CornerDownRight size={14} aria-hidden="true" /> {replyName}
              </span>
            )}
          </div>
          {identity && row.id > 0 && <ReportComment key={identity.account_id + ":" + row.id} commentId={row.id} />}
          </div>
          {row.moderation_folded ? <FoldedComment key={row.id} commentId={row.id} /> : <p>{row.content}</p>}
          <div className={s.commentActions}>
            <time dateTime={row.created_at}>{commentTime(row.created_at)}</time>
            <button
              type="button"
              className={s.commentAction}
              aria-label="回复"
              title="回复"
              disabled={sending}
              onClick={() => selectReply(root.id, row)}
            >
              <CornerDownRight size={18} aria-hidden="true" />
            </button>
            <button
              type="button"
              className={s.commentAction}
              aria-label={like.is_liked ? "取消评论点赞" : "点赞评论"}
              aria-pressed={!!like.is_liked}
              disabled={likes[row.id]?.pending}
              onClick={() => void toggleLike(row)}
            >
              <Heart
                size={18}
                fill={like.is_liked ? "currentColor" : "none"}
                aria-hidden="true"
              />
              <span>{like.likes_count ?? 0}</span>
            </button>
            {identity?.account_id === row.author_id && (
              <button
                type="button"
                className={s.commentAction}
                disabled={sending}
                onClick={(event) => {
                  removeTrigger.current = event.currentTarget;
                  setFailure(null);
                  setDeleting({ id: row.id, rootId: root.id });
                }}
              >
                删除
              </button>
            )}
          </div>
        </div>
      </div>
    );
  }

  return (
    <>
      <Modal
        title="评论"
        headingCount={total}
        inline={wide}
        open
        onClose={onClose}
      >
        <div className={s.dialogBody} ref={scrollArea}>
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
              <section
                key={root.id}
                className={s.commentThread}
                data-comment-root={root.id}
              >
                <article className={s.comment}>
                  {renderComment(root, root)}
                  {root.reply_count > 0 && !thread?.expanded && (
                    <button
                      type="button"
                      className={s.commentFold}
                      aria-expanded={false}
                      onClick={() => {
                        if (thread?.cursor !== undefined)
                          updateReplies(root.id, (old) => ({
                            ...old,
                            expanded: true,
                          }));
                        else void loadReplies(root.id);
                      }}
                    >
                      展开 {root.reply_count} 条回复{" "}
                      <ChevronDown size={16} aria-hidden="true" />
                    </button>
                  )}
                </article>
                {thread?.expanded && root.reply_count > 0 && (
                  <div className={s.commentReplies}>
                    {thread.items.map((reply) => (
                      <article key={reply.id} className={s.comment}>
                        {renderComment(root, reply)}
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
                    <button
                      type="button"
                      className={s.commentFold}
                      aria-expanded
                      onClick={() => collapse(root.id)}
                    >
                      收起回复 <ChevronUp size={16} aria-hidden="true" />
                    </button>
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
        <div className={s.commentComposer}>
          <textarea
            aria-label="你的评论"
            ref={input}
            rows={1}
            value={content}
            disabled={sending}
            onChange={(event) => setContent(event.target.value)}
            placeholder={target ? `回复 @${target.username}` : "留下一条评论…"}
          />
          <button
            type="button"
            disabled={sending || !content.trim()}
            onClick={() => void send()}
          >
            {sending ? "发送中…" : target ? "发送回复" : "发送评论"}
          </button>
        </div>
        <Dialog.Root
          open={deleting !== null}
          onOpenChange={(open) => {
            if (!open && !sending) {
              setDeleting(null);
              setFailure(null);
            }
          }}
        >
          <Dialog.Overlay className={s.commentConfirmOverlay} />
          <Dialog.Content
            className={s.commentConfirm}
            onInteractOutside={(event) => event.preventDefault()}
            onEscapeKeyDown={(event) => {
              if (sending) event.preventDefault();
            }}
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              const trigger = removeTrigger.current;
              if (trigger?.isConnected) trigger.focus();
              else input.current?.focus();
            }}
          >
            <Dialog.Title>删除这条评论？</Dialog.Title>
            <Dialog.Description>删除后无法恢复。</Dialog.Description>
            <ErrorBox error={failure} />
            <div className={s.commentConfirmActions}>
              <Dialog.Close asChild>
                <button type="button" disabled={sending}>
                  取消
                </button>
              </Dialog.Close>
              <button
                type="button"
                className={s.commentConfirmDelete}
                disabled={sending}
                onClick={() => void remove()}
              >
                {sending ? "删除中…" : "确认删除"}
              </button>
            </div>
          </Dialog.Content>
        </Dialog.Root>
      </Modal>
    </>
  );
}
