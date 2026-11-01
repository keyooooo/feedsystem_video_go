import FollowButton from "./FollowButton";
import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowLeft, Send, Settings as SettingsIcon } from "lucide-react";
import type { Account, DirectMessage } from "../api/types";
import { postJson } from "../api/client";
import * as social from "../api/social";
import * as video from "../api/video";
import * as likes from "../api/like";
import * as messages from "../api/message";
import * as accounts from "../api/account";
import {
  Avatar,
  Empty,
  ErrorBox,
  Modal,
  useIdentity,
  useLoad,
  useSession,
} from "./ui";
import { VideoCard, fromVideo } from "./VideoPages";
import s from "./App.module.css";
type Profile = {
  account: Account;
  video_count: number;
  total_likes: number;
  follower_count: number;
  vlogger_count: number;
};
export function contactsFor(
  followers: Account[],
  following: Account[],
  myId: number,
) {
  return [
    ...new Map(
      [...following, ...followers]
        .filter((a) => a.id !== myId)
        .map((a) => [a.id, a]),
    ).values(),
  ];
}
export function shouldSend(
  event: Pick<KeyboardEvent, "key" | "shiftKey" | "nativeEvent">,
) {
  return (
    event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing
  );
}
export function ProfilePage({ own = false }: { own?: boolean }) {
  const params = useParams(),
    identity = useIdentity(),
    session = useSession(),
    navigate = useNavigate(),
    id = own ? Number(identity?.account_id) : Number(params.id);
  const [tab, setTab] = useState("works"),
    [list, setList] = useState<"followers" | "following" | null>(null);
  const result = useLoad(`${id}:${session.revision}`, async () => {
    if (!Number.isSafeInteger(id) || id <= 0) throw new Error("用户暂不可用");
    return postJson<Profile>("/account/getProfile", { account_id: id });
  });
  const works = useLoad(`${id}:${tab}`, () =>
    tab === "likes" ? likes.listMyLikedVideos() : video.listByAuthorId(id),
  );
  const people = useLoad(`${id}:${list}:${session.revision}`, async () =>
    !list || !session.token
      ? []
      : list === "followers"
        ? (await social.getAllFollowers(id)).followers
        : (await social.getAllVloggers(id)).vloggers,
  );
  const profile = result.data;
  return (
    <>
      <ErrorBox error={result.error} retry={result.reload} />
      {result.loading ? (
        <div className={s.skeleton} />
      ) : profile ? (
        <>
          <div className={s.profile}>
            <Avatar
              name={profile.account.username}
              url={profile.account.avatar_url}
            />
            <div style={{ flex: 1, minWidth: 0 }}>
              <p className={s.eyebrow}>{own ? "我的创作空间" : "创作者"}</p>
              <h1>@{profile.account.username}</h1>
              <p className={s.muted}>
                {profile.account.bio || "每一个平凡的日子，都值得被记录。"}
              </p>
              <div className={s.row}>
                <span>
                  <strong>{profile.video_count}</strong> 作品
                </span>
                <span>
                  <strong>{profile.total_likes}</strong> 获赞
                </span>
                <button
                  onClick={() =>
                    session.token ? setList("followers") : navigate("/account")
                  }
                >
                  {profile.follower_count} 粉丝
                </button>
                <button
                  onClick={() =>
                    session.token ? setList("following") : navigate("/account")
                  }
                >
                  {profile.vlogger_count} 关注
                </button>
              </div>
            </div>
            {own ? (
              <Link to="/settings">
                <SettingsIcon /> 编辑资料
              </Link>
            ) : (
              identity?.account_id !== id && (
                <div className={s.row}>
                  <FollowButton id={id} onChanged={result.reload} />
                  <Link to={`/messages/${id}`}>私信 ↗</Link>
                </div>
              )
            )}
          </div>
          <div className={s.tabs}>
            {["works", ...(own ? ["likes"] : [])].map((key) => (
              <button
                key={key}
                aria-pressed={tab === key}
                onClick={() => setTab(key)}
              >
                {key === "works" ? "作品" : "喜欢"}
              </button>
            ))}
          </div>
          <ErrorBox error={works.error} retry={works.reload} />
          {works.loading ? (
            <div className={s.skeleton} />
          ) : works.data?.length ? (
            <div className={s.works}>
              {works.data.map((v) => (
                <VideoCard key={v.id} item={fromVideo(v)} />
              ))}
            </div>
          ) : (
            <Empty
              title={tab === "works" ? "还没有发布作品" : "还没有喜欢的视频"}
            >
              {own && <Link to="/video">分享第一条作品 ↗</Link>}
            </Empty>
          )}
        </>
      ) : null}
      <Modal
        title={list === "followers" ? "粉丝" : "关注"}
        open={list !== null}
        onClose={() => setList(null)}
      >
        <div className={s.dialogBody}>
          <ErrorBox error={people.error} retry={people.reload} />
          {people.loading ? (
            <div className={s.skeleton} />
          ) : people.data?.length ? (
            people.data.map((a) => (
              <Link
                className={s.contact}
                key={a.id}
                to={`/u/${a.id}`}
                onClick={() => setList(null)}
              >
                <Avatar name={a.username} url={a.avatar_url} />@{a.username}
              </Link>
            ))
          ) : (
            <Empty title="这里暂时还没有人" />
          )}
        </div>
      </Modal>
    </>
  );
}
export function MessagesPage() {
  const { peerId } = useParams(),
    id = Number(peerId || 0),
    identity = useIdentity(),
    session = useSession(),
    [content, setContent] = useState(""),
    [sending, setSending] = useState(false),
    [error, setError] = useState<unknown>(null),
    bottom = useRef<HTMLDivElement>(null),
    version = useRef(0);
  const contact = useLoad(String(session.revision), async () => {
    const [a, b] = await Promise.all([
      social.getAllFollowers(),
      social.getAllVloggers(),
    ]);
    return contactsFor(a.followers, b.vloggers, Number(identity?.account_id));
  });
  const chat = useLoad(`${id}:${session.revision}`, async () => {
    if (!id) return { peer: null, list: [] as DirectMessage[] };
    if (!Number.isSafeInteger(id) || id <= 0 || id === identity?.account_id)
      throw new Error("请选择其他用户开始聊天");
    const [peer, result] = await Promise.all([
      accounts.findById(id),
      messages.listMessages(id),
    ]);
    return { peer, list: result.messages || [] };
  });
  const [sent, setSent] = useState<DirectMessage[]>([]);
  useEffect(() => {
    version.current++;
    setContent("");
    setSent([]);
    setError(null);
  }, [id, session.revision]);
  const list = [
    ...new Map(
      [...(chat.data?.list || []), ...sent].map((m) => [m.id, m]),
    ).values(),
  ].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
  useEffect(
    () => bottom.current?.scrollIntoView({ block: "end" }),
    [list.length],
  );
  async function send() {
    if (sending || !content.trim() || !id) return;
    const captured = version.current;
    setSending(true);
    setError(null);
    try {
      const result = await messages.sendMessage(id, content.trim());
      if (captured === version.current) {
        setSent((v) => [...v, result]);
        setContent("");
      }
    } catch (e) {
      if (captured === version.current) setError(e);
    } finally {
      setSending(false);
    }
  }
  return (
    <>
      <div className={s.chat}>
        <aside className={`${s.panel} ${s.contacts}`} data-active={!!id}>
          <h1>私信</h1>
          <p className={s.muted}>从关注与粉丝中找到朋友。</p>
          <ErrorBox error={contact.error} retry={contact.reload} />
          {contact.data?.map((a) => (
            <Link className={s.contact} key={a.id} to={`/messages/${a.id}`}>
              <Avatar name={a.username} />
              <span>@{a.username}</span>
            </Link>
          ))}
          {!contact.loading && !contact.data?.length && (
            <Empty title="还没有联系人" />
          )}
        </aside>
        <section className={`${s.panel} ${s.conversation}`} data-active={!!id}>
          {id ? (
            <>
              <header className={s.row}>
                <Link to="/messages" aria-label="返回联系人">
                  <ArrowLeft />
                </Link>
                {chat.data?.peer && (
                  <Link to={`/u/${id}`}>
                    <strong>@{chat.data.peer.username}</strong>
                  </Link>
                )}
                <button onClick={chat.reload} disabled={chat.loading}>
                  刷新
                </button>
              </header>
              <ErrorBox error={chat.error} retry={chat.reload} />
              <div className={s.messages}>
                {chat.loading ? (
                  <div className={s.skeleton} />
                ) : list.length ? (
                  list.map((m, i) => (
                    <div key={m.id}>
                      {(!i ||
                        list[i - 1]?.created_at.slice(0, 10) !==
                          m.created_at.slice(0, 10)) && (
                        <p className={s.muted} style={{ textAlign: "center" }}>
                          {new Date(m.created_at).toLocaleDateString()}
                        </p>
                      )}
                      <div
                        className={`${s.bubble} ${m.from_id === identity?.account_id ? s.mine : ""}`}
                      >
                        {m.content}
                        <div>
                          <small>
                            {new Date(m.created_at).toLocaleTimeString([], {
                              hour: "2-digit",
                              minute: "2-digit",
                            })}
                          </small>
                        </div>
                      </div>
                    </div>
                  ))
                ) : (
                  <Empty title="从一句问候开始" />
                )}
                <div ref={bottom} />
              </div>
              <ErrorBox error={error} />
              <form
                className={s.composer}
                onSubmit={(e: FormEvent) => {
                  e.preventDefault();
                  void send();
                }}
              >
                <textarea
                  aria-label="消息内容"
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  onKeyDown={(e) => {
                    if (shouldSend(e)) {
                      e.preventDefault();
                      void send();
                    }
                  }}
                  placeholder="发送消息…"
                />
                <button
                  className={s.primary}
                  aria-label="发送消息"
                  disabled={sending || !content.trim() || !chat.data?.peer}
                >
                  <Send size={20} />
                </button>
              </form>
            </>
          ) : (
            <Empty title="选择一位朋友，开始聊天" />
          )}
        </section>
      </div>
    </>
  );
}
