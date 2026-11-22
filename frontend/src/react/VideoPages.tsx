import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  Heart,
  MessageCircle,
  Share2,
  Play,
  Pause,
  Maximize,
  Minimize,
  Volume2,
  VolumeX,
} from "lucide-react";
import type { FeedVideoItem, Video } from "../api/types";
import { findById } from "../api/account";
import { getJson } from "../api/client";
import * as feed from "../api/feed";
import * as video from "../api/video";
import * as like from "../api/like";
import { CommentPanel } from "./CommentPanel";
export { CommentPanel } from "./CommentPanel";
import {
  Avatar,
  Empty,
  ErrorBox,
  Modal,
  useSession,
  useLoad,
  useNotice,
  useMedia,
} from "./ui";
import { mediaPath } from "./directUploadConfig";
import {
  applyVideoLike,
  confirmVideoLike,
  useVideoLikes,
} from "./video-like-state";
import s from "./App.module.css";
export type FeedMode = "latest" | "following" | "hot" | "ranking";
export type Cursor = {
  next_time?: number;
  next_likes_count_before?: number;
  next_id_before?: number;
  as_of?: number;
  next_offset?: number;
  next_latest_popularity?: number;
  next_latest_before?: string;
  next_latest_id_before?: number;
};
export function pagingInput(mode: FeedMode, cursor: Cursor, reset: boolean) {
  if (mode === "latest" || mode === "following")
    return { limit: 10, latest_time: reset ? 0 : (cursor.next_time ?? 0) };
  if (mode === "ranking")
    return {
      limit: 10,
      ...(!reset && cursor.next_id_before
        ? {
            likes_count_before: cursor.next_likes_count_before ?? 0,
            id_before: cursor.next_id_before,
          }
        : {}),
    };
  return {
    limit: 10,
    as_of: reset ? 0 : (cursor.as_of ?? 0),
    offset: reset ? 0 : (cursor.next_offset ?? 0),
    ...(!reset && cursor.next_latest_before && cursor.next_latest_id_before
      ? {
          latest_popularity: cursor.next_latest_popularity ?? 0,
          latest_before: cursor.next_latest_before,
          latest_id_before: cursor.next_latest_id_before,
        }
      : {}),
  };
}
async function fetchPage(mode: FeedMode, cursor: Cursor, reset: boolean) {
  const input = pagingInput(mode, cursor, reset);
  if (mode === "ranking") return feed.listLikesCount(input);
  if ("as_of" in input) return feed.listByPopularity(input);
  const chronological = {
    limit: input.limit,
    latest_time: input.latest_time ?? 0,
  };
  return mode === "following"
    ? feed.listByFollowing(chronological)
    : feed.listLatest(chronological);
}
function useFeed(mode: FeedMode) {
  const session = useSession();
  const [items, setItems] = useState<FeedVideoItem[]>([]),
    [loading, setLoading] = useState(true),
    [error, setError] = useState<unknown>(null),
    [more, setMore] = useState(false);
  const cursor = useRef<Cursor>({}),
    busy = useRef(false),
    generation = useRef(0);
  const load = useCallback(
    async (reset = false) => {
      if (busy.current) return;
      const version = generation.current;
      busy.current = true;
      setLoading(true);
      setError(null);
      try {
        const result = await fetchPage(mode, cursor.current, reset);
        if (version !== generation.current) return;
        // Stop when the server offers the same cursor twice.
        const nextPage = pagingInput(mode, result, false);
        const previousPage = pagingInput(mode, cursor.current, reset);
        cursor.current = result;
        setMore(
          result.has_more &&
            JSON.stringify(nextPage) !== JSON.stringify(previousPage),
        );
        setItems((old) => {
          const map = new Map((reset ? [] : old).map((v) => [v.id, v]));
          for (const item of result.video_list) map.set(item.id, item);
          return [...map.values()];
        });
      } catch (e) {
        if (version === generation.current) setError(e);
      } finally {
        if (version === generation.current) {
          busy.current = false;
          setLoading(false);
        }
      }
    },
    [mode],
  );
  useEffect(() => {
    const epoch = ++generation.current;
    busy.current = false;
    cursor.current = {};
    setItems([]);
    setMore(false);
    void load(true);
    return () => {
      generation.current = epoch + 1;
    };
  }, [load, session.revision]);
  return { items, setItems, loading, error, more, load };
}
export function Cover({ url, title }: { url: string; title: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [url]);
  return url && !failed ? (
    <img
      className={s.cover}
      src={mediaPath(url)}
      alt={title}
      loading="lazy"
      onError={() => setFailed(true)}
    />
  ) : (
    <div className={s.cover}>
      <Play aria-hidden="true" />
      <span>暂无封面</span>
    </div>
  );
}
export function VideoCard({
  item,
  rank,
  profile = false,
  showAuthor = false,
}: {
  item: FeedVideoItem;
  rank?: number;
  profile?: boolean;
  showAuthor?: boolean;
}) {
  const session = useSession();
  const confirmation = useVideoLikes((state) => state[item.id]);
  item = applyVideoLike(item, confirmation, session.revision);
  if (profile) {
    return (
      <article className={s.workCard}>
        <Link to={`/video/${item.id}`} aria-label={item.title}>
          <div className={s.workCover}>
            <Cover url={item.cover_url} title={item.title} />
            <span
              className={s.workLikes}
              role="img"
              aria-label={`${item.is_liked ? "已赞" : "未赞"}，${item.likes_count} 个赞`}
            >
              <Heart
                className={item.is_liked ? s.liked : undefined}
                fill={item.is_liked ? "currentColor" : "none"}
                aria-hidden="true"
              />
              {item.likes_count}
            </span>
          </div>
          <h3>{item.title}</h3>
          {showAuthor && <small>@{item.author.username}</small>}
        </Link>
      </article>
    );
  }
  return (
    <article className={s.card}>
      <Link to={`/video/${item.id}`}>
        <Cover url={item.cover_url} title={item.title} />
        <div className={s.cardBody}>
          {rank && (
            <small className={s.eyebrow}>
              TOP {String(rank).padStart(2, "0")}
            </small>
          )}
          <h3>{item.title}</h3>
          <div className={s.row}>
            <small>@{item.author.username}</small>
            <small>♡ {item.likes_count}</small>
          </div>
        </div>
      </Link>
    </article>
  );
}
export function fromVideo(item: Video): FeedVideoItem {
  return {
    ...item,
    author: { id: item.author_id, username: item.username },
    create_time: new Date(item.create_time).getTime() / 1000,
    is_liked: item.is_liked ?? false,
  };
}
function playbackTime(seconds: number) {
  const whole = Math.floor(seconds);
  const minutes = Math.floor(whole / 60);
  return `${String(minutes).padStart(2, "0")}:${String(whole % 60).padStart(2, "0")}`;
}

function Player({
  item,
  active,
  muted,
  presentation,
}: {
  item: FeedVideoItem;
  active: boolean;
  muted: boolean;
  presentation?: {
    metadata: ReactNode;
    actions: ReactNode;
    toggleMute: () => void;
  };
}) {
  const ref = useRef<HTMLVideoElement>(null);
  const stage = useRef<HTMLDivElement>(null);
  const clickToPlay = import.meta.env.MODE === "demo";
  const [blocked, setBlocked] = useState(clickToPlay);
  const [playing, setPlaying] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [shape, setShape] = useState<"unknown" | "wide" | "square" | "tall">(
    "unknown",
  );
  const wantsPlayback = useRef(false);
  const retrySignedURL = useRef<(() => boolean) | undefined>(undefined);
  const seekSigned = useRef<((time: number) => boolean) | undefined>(undefined);
  const [duration, setDuration] = useState(0);
  const [position, setPosition] = useState(0);
  const source = mediaPath(item.play_url);
  const poster = mediaPath(item.cover_url);
  const immersive = !!presentation;
  const signed =
    !!source && /^\/api\/media\/assets\/[a-f0-9]{32}$/.test(source);
  useEffect(() => {
    const element = ref.current;
    if (!element || !source) return;
    let lastRequest = Date.now();
    let refreshed = false;
    let lastTime = 0;
    let pendingTime: number | undefined;
    let live = true;
    let resolving = false;
    const controller = new AbortController();
    const playbackOrigin =
      import.meta.env.VITE_OSS_PLAYBACK_ORIGIN ||
      import.meta.env.VITE_OSS_UPLOAD_ORIGIN;

    const sample = () => {
      if (pendingTime !== undefined) return;
      lastTime = element.currentTime;
      setPosition(lastTime);
    };
    const retry = (time = element.currentTime || pendingTime || lastTime) => {
      if (!signed || refreshed || resolving) return false;
      refreshed = true;
      pendingTime = time;
      resolving = true;
      void getJson<{ url: string }>(`${source.slice(4)}?resolve=1`, {
        signal: controller.signal,
      })
        .then(({ url }) => {
          if (!live) return;
          const address = new URL(url, location.origin);
          if (
            address.origin !== playbackOrigin &&
            address.origin !== location.origin
          )
            throw new Error("播放域名与站点配置不匹配");
          lastRequest = Date.now();
          // A paused element needs playable data from the new source before seeking.
          element.preload = "auto";
          element.src = address.href;
          element.load();
        })
        .catch(() => {
          if (!live) return;
          pendingTime = undefined;
          setBlocked(true);
        })
        .finally(() => {
          resolving = false;
        });
      return true;
    };
    const seek = (time: number) => {
      if (Date.now() - lastRequest < 14 * 60_000) {
        if (pendingTime === undefined) return false;
        pendingTime = time;
        return true;
      }
      refreshed = false;
      return retry(time);
    };
    const error = () => {
      if (!retry()) setBlocked(true);
    };
    const restore = () => {
      if (pendingTime === undefined) return;
      // WebKit may reset a seek made at loadedmetadata while it opens the new source.
      const time = Math.min(pendingTime, element.duration || pendingTime);
      pendingTime = undefined;
      if (time > 0) element.currentTime = time;
      setPosition(time);
      if (wantsPlayback.current) {
        void element.play().then(
          () => {
            if (live) setBlocked(false);
          },
          () => {
            if (live) setBlocked(true);
          },
        );
      }
    };
    if (signed) {
      retrySignedURL.current = () => {
        refreshed = false;
        return retry();
      };
      seekSigned.current = seek;
      element.addEventListener("timeupdate", sample);
      element.addEventListener("error", error);
      element.addEventListener("canplay", restore);
    }
    if (!clickToPlay) element.src = source;
    return () => {
      live = false;
      controller.abort();
      retrySignedURL.current = undefined;
      seekSigned.current = undefined;
      element.removeEventListener("timeupdate", sample);
      element.removeEventListener("error", error);
      element.removeEventListener("canplay", restore);
      element.pause();
      element.removeAttribute("src");
      element.load();
    };
  }, [source, signed, clickToPlay]);
  useEffect(() => {
    if (ref.current) ref.current.muted = muted;
  }, [muted]);
  useEffect(() => {
    if (!immersive || !active) return;
    const update = () =>
      setFullscreen(document.fullscreenElement === stage.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, [immersive, active]);
  useEffect(() => {
    const element = ref.current;
    if (!element) return;
    let live = true;
    wantsPlayback.current = active && !clickToPlay;
    if (active && !clickToPlay)
      void element
        .play()
        .then(() => {
          if (live) setBlocked(false);
        })
        .catch(() => {
          if (live) setBlocked(true);
        });
    else element.pause();
    return () => {
      live = false;
      element.pause();
    };
  }, [source, active, clickToPlay]);
  function play() {
    const element = ref.current;
    if (!element) return;
    wantsPlayback.current = true;
    if (clickToPlay && source && !element.hasAttribute("src"))
      element.src = source;
    if ((!element.currentSrc || element.error) && retrySignedURL.current?.())
      return;
    void element.play().then(
      () => setBlocked(false),
      () => setBlocked(true),
    );
  }
  function togglePlayback() {
    const element = ref.current;
    if (!element) return;
    if (element.paused) play();
    else {
      wantsPlayback.current = false;
      element.pause();
      if (!presentation) setBlocked(true);
    }
  }

  function toggleFullscreen() {
    const element = ref.current as
      (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null;
    if (document.fullscreenElement) {
      void document.exitFullscreen().catch(() => {});
    } else if (document.fullscreenEnabled && stage.current?.requestFullscreen) {
      void stage.current.requestFullscreen().catch(() => {});
    } else {
      element?.webkitEnterFullscreen?.();
    }
  }

  const videoElement = (
    <video
      ref={ref}
      playsInline
      loop
      muted={muted}
      poster={poster}
      preload={active && !clickToPlay ? "metadata" : "none"}
      aria-label={item.title}
      onDurationChange={(event) => {
        const value = event.currentTarget.duration;
        if (Number.isFinite(value)) setDuration(value);
      }}
      onLoadedMetadata={({ currentTarget: { videoWidth, videoHeight } }) => {
        if (videoWidth && videoHeight)
          setShape(
            videoWidth === videoHeight
              ? "square"
              : videoWidth > videoHeight
                ? "wide"
                : "tall",
          );
      }}
      onTimeUpdate={
        signed
          ? undefined
          : (event) => setPosition(event.currentTarget.currentTime)
      }
      onPlay={() => setPlaying(true)}
      onPause={() => setPlaying(false)}
      onClick={togglePlayback}
    />
  );
  const canSeek = active && duration > 0;
  const seekBar = (
    <input
      aria-label="播放进度"
      className={s.seek}
      type="range"
      min={0}
      max={duration || 1}
      step={0.01}
      value={Math.min(position, duration)}
      disabled={!canSeek}
      onChange={(event) => {
        const time = event.currentTarget.valueAsNumber;
        setPosition(time);
        if (ref.current && !seekSigned.current?.(time))
          ref.current.currentTime = time;
      }}
    />
  );
  const playOverlay = blocked && active && (
    <button className={`${s.primary} ${s.playOverlay}`} onClick={play}>
      <Play size={18} /> 点击播放
    </button>
  );
  if (presentation) {
    return (
      <div
        className={s.homeStage}
        ref={stage}
        data-video-shape={shape}
        data-player
      >
        <div className={s.playerVisual}>
          {poster && shape !== "wide" && (
            <img
              key={poster}
              src={poster}
              alt=""
              aria-hidden="true"
              className={s.playerBackdrop}
              onError={(event) => {
                event.currentTarget.hidden = true;
              }}
            />
          )}
          {videoElement}
          {playOverlay}
        </div>
        {presentation.metadata}
        {presentation.actions}
        <div className={s.playerControls}>
          {seekBar}
          <div className={s.playerToolbar}>
            <button
              type="button"
              aria-label={playing ? "暂停" : "播放"}
              onClick={togglePlayback}
            >
              {playing ? <Pause size={21} /> : <Play size={21} />}
            </button>
            <span>
              {playbackTime(position)} / {playbackTime(duration)}
            </span>
            <button
              type="button"
              aria-label={muted ? "取消静音" : "静音"}
              onClick={presentation.toggleMute}
            >
              {muted ? <VolumeX size={21} /> : <Volume2 size={21} />}
            </button>
            <button
              type="button"
              aria-label={fullscreen ? "退出全屏" : "全屏"}
              onClick={toggleFullscreen}
            >
              {fullscreen ? <Minimize size={20} /> : <Maximize size={20} />}
            </button>
          </div>
        </div>
      </div>
    );
  }
  return (
    <>
      {videoElement}
      {signed && canSeek && seekBar}
      {playOverlay}
    </>
  );
}
function useVideoActions(onConfirmed: (item: FeedVideoItem) => void) {
  const session = useSession(),
    navigate = useNavigate(),
    show = useNotice((v) => v.show);
  const [busy, setBusy] = useState<Record<number, boolean>>({});
  const [shareUrl, setShareUrl] = useState("");
  const locks = useRef(new Set<number>());
  async function toggle(item: FeedVideoItem) {
    if (!session.token) {
      navigate(
        "/account?redirect=" +
          encodeURIComponent(location.pathname + location.search),
      );
      return;
    }
    if (locks.current.has(item.id)) return;
    locks.current.add(item.id);
    setBusy((v) => ({ ...v, [item.id]: true }));
    try {
      if (item.is_liked) await like.unlike(item.id);
      else await like.like(item.id);
      if (confirmVideoLike(item, session.revision)) {
        onConfirmed({
          ...item,
          is_liked: !item.is_liked,
          likes_count: Math.max(0, item.likes_count + (item.is_liked ? -1 : 1)),
        });
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "操作失败");
    } finally {
      locks.current.delete(item.id);
      setBusy((v) => ({ ...v, [item.id]: false }));
    }
  }
  async function share(item: FeedVideoItem) {
    try {
      await navigator.clipboard.writeText(
        `${location.origin}/video/${item.id}`,
      );
      show("链接已复制");
    } catch {
      setShareUrl(`${location.origin}/video/${item.id}`);
    }
  }
  return {
    toggle,
    share,
    busy,
    sharePanel: (
      <Modal title="分享视频" open={!!shareUrl} onClose={() => setShareUrl("")}>
        <p>选中下方链接后复制，发送给朋友。</p>
        <label>
          视频链接
          <input
            readOnly
            autoFocus
            value={shareUrl}
            onFocus={(e) => e.currentTarget.select()}
          />
        </label>
      </Modal>
    ),
  };
}
type AuthorCache = Map<number, ReturnType<typeof findById>>;

function AuthorAvatar({
  author,
  cache,
}: {
  author: FeedVideoItem["author"];
  cache: AuthorCache;
}) {
  const profile = useLoad(String(author.id), () => {
    const cached = cache.get(author.id);
    if (cached) return cached;
    const request = findById(author.id).catch((error: unknown) => {
      cache.delete(author.id);
      throw error;
    });
    cache.set(author.id, request);
    return request;
  });

  return (
    <Link
      to={`/u/${author.id}`}
      aria-label={`查看 ${author.username} 的主页`}
      className={s.authorLink}
    >
      <Avatar name={author.username} url={profile.data?.avatar_url} />
    </Link>
  );
}

function VideoStage({
  item,
  active,
  muted,
  onMute,
  authorCache,
  actions,
  onComment,
  detail = false,
  likeUnavailable = false,
}: {
  item: FeedVideoItem;
  active: boolean;
  muted: boolean;
  onMute: () => void;
  authorCache: AuthorCache;
  actions: ReturnType<typeof useVideoActions>;
  onComment: () => void;
  detail?: boolean;
  likeUnavailable?: boolean;
}) {
  const session = useSession();
  const confirmation = useVideoLikes((state) => state[item.id]);
  const display = applyVideoLike(item, confirmation, session.revision);
  return (
    <Player
      item={item}
      active={active}
      muted={muted}
      presentation={{
        toggleMute: onMute,
        metadata: (
          <div className={s.homeMeta}>
            <Link to={`/u/${item.author.id}`}>@{item.author.username}</Link>
            {detail ? (
              <h1>{item.title}</h1>
            ) : (
              <h2>
                <Link to={`/video/${item.id}`}>{item.title}</Link>
              </h2>
            )}
            {item.description && <p>{item.description}</p>}
          </div>
        ),
        actions: (
          <div className={s.homeActions} role="group" aria-label="视频互动">
            {active && (
              <AuthorAvatar author={item.author} cache={authorCache} />
            )}
            <button
              type="button"
              aria-label={display.is_liked ? "取消点赞" : "点赞"}
              aria-pressed={display.is_liked}
              disabled={likeUnavailable || actions.busy[item.id]}
              onClick={() => void actions.toggle(display)}
            >
              <Heart fill={display.is_liked ? "currentColor" : "none"} />
              <span>{display.likes_count}</span>
            </button>
            <button type="button" aria-label="打开评论" onClick={onComment}>
              <MessageCircle />
              <span>评论</span>
            </button>
            <button
              type="button"
              aria-label="分享视频"
              onClick={() => void actions.share(item)}
            >
              <Share2 />
              <span>分享</span>
            </button>
          </div>
        ),
      }}
    />
  );
}

export function FeedPage({ ranking = false }: { ranking?: boolean }) {
  const [query, setQuery] = useSearchParams();
  const selected = query.get("feed");
  const mode = ranking
    ? "ranking"
    : selected === "following" || selected === "hot"
      ? selected
      : "latest";
  const heading =
    mode === "following"
      ? "关注动态"
      : mode === "hot"
        ? "热榜"
        : mode === "ranking"
          ? "点赞榜"
          : "推荐";
  const keyword = (query.get("q") ?? "").trim().toLowerCase();
  const state = useFeed(mode);
  const items = state.items.filter((item) =>
    `${item.title} ${item.author.username}`.toLowerCase().includes(keyword),
  );
  const [active, setActive] = useState(0);
  const [height, setHeight] = useState(0);
  const [muted, setMuted] = useState(true);
  const [drawer, setDrawer] = useState<FeedVideoItem | null>(null);
  const [authorCache] = useState<AuthorCache>(() => new Map());
  const scroll = useRef<HTMLDivElement>(null);
  const previousHeight = useRef(0);
  const actions = useVideoActions((updated) =>
    state.setItems((items) =>
      items.map((item) => (item.id === updated.id ? updated : item)),
    ),
  );
  const hasPlayer = !ranking && items.length > 0;
  const current = Math.min(active, Math.max(0, items.length - 1));
  const { more, loading, load, error } = state;

  useEffect(() => {
    setActive(0);
    setDrawer(null);
    scroll.current?.scrollTo(0, 0);
  }, [mode, keyword]);

  useLayoutEffect(() => {
    const element = scroll.current;
    if (!hasPlayer || !element) return;
    const measure = () => setHeight(element.clientHeight);
    const observer = new ResizeObserver(measure);
    measure();
    observer.observe(element);
    return () => observer.disconnect();
  }, [hasPlayer]);

  useLayoutEffect(() => {
    // Maintain the active slide when the viewport height changes.
    if (previousHeight.current === height) return;
    previousHeight.current = height;
    if (scroll.current && height) scroll.current.scrollTop = current * height;
  }, [height, current]);

  useEffect(() => {
    if (
      hasPlayer &&
      !keyword &&
      current >= items.length - 3 &&
      more &&
      !loading &&
      !error
    )
      void load();
  }, [current, items.length, more, loading, hasPlayer, keyword, load, error]);

  useLayoutEffect(() => {
    if (!hasPlayer || drawer) return;
    function key(event: KeyboardEvent) {
      if (
        event.defaultPrevented ||
        (event.target instanceof Element &&
          event.target.closest("input,textarea,button,a,[contenteditable]")) ||
        document.fullscreenElement ||
        document.querySelector('[role="dialog"]')
      )
        return;
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        const target = Math.max(
          0,
          Math.min(
            items.length - 1,
            current + (event.key === "ArrowDown" ? 1 : -1),
          ),
        );
        const viewport = scroll.current;
        if (!viewport) return;
        viewport.scrollTo({
          top: target * viewport.clientHeight,
          behavior: matchMedia("(prefers-reduced-motion: reduce)").matches
            ? "instant"
            : "smooth",
        });
      }
    }
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [current, items.length, drawer, hasPlayer]);

  const start = Math.max(0, current - 1);
  const end = Math.min(items.length, current + 2);
  const searching = keyword && more && hasPlayer;
  const loadingMore = loading && state.items.length > 0;
  return (
    <section
      aria-label={heading}
      className={hasPlayer ? s.feedSection : undefined}
    >
      {hasPlayer && (error || loadingMore || searching) && (
        <div className={s.feedStatus}>
          <ErrorBox error={error} retry={() => void load()} />
          {loadingMore && <span role="status">正在加载更多…</span>}
          {searching && (
            <button
              type="button"
              disabled={loading}
              onClick={() => void load()}
            >
              继续查找
            </button>
          )}
        </div>
      )}
      {!hasPlayer && (
        <ErrorBox error={error} retry={() => void load(!state.items.length)} />
      )}
      {loading && !state.items.length ? (
        <div className={s.skeleton} role="status" aria-label="正在加载视频" />
      ) : !items.length ? (
        !error && (
          <Empty
            title={
              keyword
                ? "没有找到匹配的视频"
                : mode === "following"
                  ? "暂无关注动态"
                  : `${heading}暂无视频`
            }
          >
            {keyword && (
              <div className={s.row}>
                <button
                  type="button"
                  onClick={() => {
                    const params = new URLSearchParams(query);
                    params.delete("q");
                    setQuery(params);
                  }}
                >
                  清除关键词
                </button>
                {more && (
                  <button
                    type="button"
                    disabled={loading}
                    onClick={() => void load()}
                  >
                    继续查找
                  </button>
                )}
              </div>
            )}
          </Empty>
        )
      ) : ranking ? (
        <div className={s.grid}>
          {items.map((item, index) => (
            <VideoCard key={item.id} item={item} rank={index + 1} />
          ))}
        </div>
      ) : (
        <div
          ref={scroll}
          data-feed-scroll
          data-comments-open={!!drawer}
          className={s.feed}
          tabIndex={0}
          role="region"
          aria-label="视频流"
          onScroll={(event) => {
            const { scrollTop, clientHeight } = event.currentTarget;
            if (clientHeight > 0)
              setActive(
                Math.min(
                  items.length - 1,
                  Math.max(0, Math.round(scrollTop / clientHeight)),
                ),
              );
          }}
        >
          <div style={{ height: start * height }} />
          {items.slice(start, end).map((item, index) => {
            const visible = start + index === current;
            return (
              <section
                key={item.id}
                className={s.feedSlide}
                inert={!visible}
                aria-hidden={!visible}
              >
                <VideoStage
                  item={item}
                  active={visible}
                  muted={muted}
                  onMute={() => setMuted((value) => !value)}
                  authorCache={authorCache}
                  actions={actions}
                  onComment={() => setDrawer(item)}
                />
              </section>
            );
          })}
          <div style={{ height: Math.max(0, items.length - end) * height }} />
        </div>
      )}
      {ranking && more && (
        <button
          style={{ marginTop: 24 }}
          disabled={loading}
          onClick={() => void load()}
        >
          {loading ? "加载中…" : "发现更多"}
        </button>
      )}
      {actions.sharePanel}
      <CommentPanel item={drawer} onClose={() => setDrawer(null)} />
    </section>
  );
}
export function DetailPage() {
  const wide = useMedia("(min-width: 1200px)");
  const { id } = useParams();
  const session = useSession();
  const result = useLoad(String(id), async () =>
    fromVideo(await video.getDetail(Number(id))),
  );
  const status = useLoad(`${id}:${session.revision}:${!!session.token}`, () =>
    session.token
      ? like.isLiked(Number(id))
      : Promise.resolve({ is_liked: false }),
  );
  const [drawer, setDrawer] = useState(false);
  const [muted, setMuted] = useState(true);
  const [authorCache] = useState<AuthorCache>(() => new Map());
  useEffect(() => setDrawer(wide), [id, wide]);
  const [confirmed, setConfirmed] = useState<{
    revision: number;
    item: FeedVideoItem;
  } | null>(null);
  const actions = useVideoActions((item) =>
    setConfirmed({ revision: session.revision, item }),
  );
  const item =
    result.data &&
    (confirmed?.revision === session.revision &&
    confirmed.item.id === result.data.id
      ? confirmed.item
      : { ...result.data, is_liked: status.data?.is_liked ?? false });
  return (
    <>
      {actions.sharePanel}
      <ErrorBox error={result.error} retry={result.reload} />
      {result.loading ? (
        <div className={s.skeleton} />
      ) : item ? (
        <>
          <div className={`${s.feedSection} ${s.detailStage}`}>
            {status.error != null && (
              <div className={s.feedStatus}>
                <ErrorBox error={status.error} retry={status.reload} />
              </div>
            )}
            <VideoStage
              key={item.id}
              item={item}
              active
              muted={muted}
              onMute={() => setMuted((value) => !value)}
              authorCache={authorCache}
              detail
              actions={actions}
              onComment={() => setDrawer(true)}
              likeUnavailable={!!session.token && !status.data}
            />
          </div>
          <CommentPanel
            item={drawer ? item : null}
            onClose={() => setDrawer(false)}
          />
        </>
      ) : (
        <Empty title="视频暂不可用" />
      )}
    </>
  );
}
