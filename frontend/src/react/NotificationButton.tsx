import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  Bell,
  CheckCheck,
  ChevronRight,
  Heart,
  MessageCircle,
  UserRoundPlus,
} from "lucide-react";
import { findById } from "../api/account";
import { postJson } from "../api/client";
import { Avatar, ErrorBox, Modal, useLoad, useSession } from "./ui";
import { useNotificationStream } from "./useNotificationStream";
import s from "./App.module.css";

type Notification = {
  id: number;
  sender_id: number;
  target_id: number;
  type: string;
  content: string;
  is_read: boolean;
  created_at: string;
};

type Profiles = Map<
  number,
  Promise<Awaited<ReturnType<typeof findById>> | null>
>;

function NotificationRow({
  item,
  profiles,
  busy,
  onSelect,
}: {
  item: Notification;
  profiles: Profiles;
  busy: boolean;
  onSelect: (item: Notification, destination?: string) => void;
}) {
  const profile = useLoad(String(item.sender_id), () => {
    if (!item.sender_id) return Promise.resolve(null);
    const cached = profiles.get(item.sender_id);
    if (cached) return cached;
    const request = findById(item.sender_id).catch(() => {
      profiles.delete(item.sender_id);
      return null;
    });
    profiles.set(item.sender_id, request);
    return request;
  });
  const name =
    profile.data?.username ??
    (item.sender_id ? `用户 #${item.sender_id}` : "通知");
  const Icon =
    item.type === "follow"
      ? UserRoundPlus
      : item.type === "like"
        ? Heart
        : MessageCircle;
  const destination =
    item.type === "follow" && item.sender_id
      ? `/u/${item.sender_id}`
      : item.type === "moderation_report" && item.target_id
        ? `/reports/${item.target_id}`
        : item.target_id && ["like", "comment", "mention"].includes(item.type)
        ? `/video/${item.target_id}`
        : undefined;
  const date = new Date(item.created_at);
  const content = (
    <>
      <span className={s.notificationAvatar}>
        <Avatar name={name} url={profile.data?.avatar_url} />
        <Icon size={13} aria-hidden="true" />
      </span>
      <span className={s.notificationText}>
        <strong>{name}</strong>
        <span>{item.content}</span>
        {Number.isFinite(date.getTime()) && date.getUTCFullYear() > 1 && (
          <time dateTime={item.created_at}>{date.toLocaleString("zh-CN")}</time>
        )}
      </span>
      {!item.is_read && (
        <span className={s.notificationUnread} aria-label="未读" />
      )}
      {destination && <ChevronRight size={16} aria-hidden="true" />}
    </>
  );
  return (
    <li data-unread={!item.is_read}>
      {destination || !item.is_read ? (
        <button
          className={s.notificationRow}
          type="button"
          disabled={busy}
          onClick={() => onSelect(item, destination)}
        >
          {content}
        </button>
      ) : (
        <div className={s.notificationRow}>{content}</div>
      )}
    </li>
  );
}

export function NotificationButton({ signIn }: { signIn: string }) {
  const session = useSession();
  if (!session.token) {
    return (
      <Link to={signIn} className={s.headerAction} aria-label="通知">
        <Bell size={20} aria-hidden="true" />
        <span>通知</span>
      </Link>
    );
  }
  return <SignedInNotifications key={session.revision} token={session.token} />;
}

function SignedInNotifications({ token }: { token: string }) {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [failure, setFailure] = useState<unknown>(null);
  const [profiles] = useState<Profiles>(() => new Map());
  const trigger = useRef<HTMLButtonElement>(null);
  const unread = useLoad(
    "unread",
    () =>
      postJson<{ count: number }>(
        "/notification/unreadCount",
        {},
        { authRequired: true },
      ),
    true,
  );
  const list = useLoad(
    String(open),
    () =>
      open
        ? postJson<{ notifications: Notification[] }>(
            "/notification/list",
            {},
            { authRequired: true },
          )
        : Promise.resolve({ notifications: [] }),
    true,
  );
  const { reload: reloadUnread } = unread;
  const { reload: reloadList } = list;
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);
  const sync = useCallback(() => {
    reloadUnread();
    if (openRef.current) reloadList();
  }, [reloadList, reloadUnread]);
  useNotificationStream(token, sync);

  async function select(item?: Notification, destination?: string) {
    if (pending) return;
    if (!item?.is_read) {
      setPending(true);
      setFailure(null);
      try {
        await postJson("/notification/markRead", item ? { id: item.id } : {}, {
          authRequired: true,
        });
        sync();
      } catch (error) {
        setFailure(error);
        return;
      } finally {
        setPending(false);
      }
    }
    if (destination) {
      setOpen(false);
      void navigate(destination);
    }
  }

  const count = unread.data?.count ?? 0;
  return (
    <>
      <button
        ref={trigger}
        type="button"
        className={s.headerAction}
        aria-label={count ? `通知，${count} 条未读` : "通知"}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          setFailure(null);
          setOpen(true);
          reloadUnread();
        }}
      >
        <Bell size={20} aria-hidden="true" />
        {count > 0 && (
          <i className={s.notificationBadge} aria-hidden="true">
            {count > 99 ? "99+" : count}
          </i>
        )}
        <span>通知</span>
      </button>
      <Modal
        title="通知"
        open={open}
        onClose={() => {
          setOpen(false);
          requestAnimationFrame(() => trigger.current?.focus());
        }}
      >
        <div className={s.notificationToolbar}>
          <span>{count ? `${count} 条未读` : "最近通知"}</span>
          <button
            type="button"
            disabled={pending || !count}
            onClick={() => void select()}
          >
            <CheckCheck size={17} aria-hidden="true" /> 全部已读
          </button>
        </div>
        <div className={s.dialogBody}>
          {list.loading && !list.data && <p role="status">正在加载通知…</p>}
          <ErrorBox error={list.error} retry={reloadList} />
          <ErrorBox error={failure} />
          {list.data?.notifications.length === 0 && (
            <p className={s.notificationEmpty}>暂无通知</p>
          )}
          <ul className={s.notificationList}>
            {list.data?.notifications.map((item) => (
              <NotificationRow
                key={item.id}
                item={item}
                profiles={profiles}
                busy={pending}
                onSelect={(row, url) => void select(row, url)}
              />
            ))}
          </ul>
        </div>
      </Modal>
    </>
  );
}
