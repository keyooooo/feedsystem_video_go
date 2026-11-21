import { useState } from "react";
import { Link } from "react-router-dom";
import { Bell } from "lucide-react";
import { postJson } from "../api/client";
import { ErrorBox, Modal, useLoad, useSession } from "./ui";
import s from "./App.module.css";

type Notification = {
  id: number;
  content: string;
  is_read: boolean;
  created_at: string;
};

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
  return <SignedInNotifications key={session.revision} />;
}

function SignedInNotifications() {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<number | null>(null);
  const [failure, setFailure] = useState<unknown>(null);
  const unread = useLoad("unread", () =>
    postJson<{ count: number }>(
      "/notification/unreadCount",
      {},
      { authRequired: true },
    ),
  );
  const list = useLoad(String(open), () =>
    open
      ? postJson<{ notifications: Notification[] }>(
          "/notification/list",
          {},
          { authRequired: true },
        )
      : Promise.resolve({ notifications: [] }),
  );

  async function markRead(item: Notification) {
    if (item.is_read || pending !== null) return;
    setPending(item.id);
    setFailure(null);
    try {
      await postJson(
        "/notification/markRead",
        { id: item.id },
        { authRequired: true },
      );
      list.reload();
      unread.reload();
    } catch (error) {
      setFailure(error);
    } finally {
      setPending(null);
    }
  }

  return (
    <>
      <button
        type="button"
        className={s.headerAction}
        aria-label={
          unread.data?.count ? `通知，${unread.data.count} 条未读` : "通知"
        }
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          setFailure(null);
          setOpen(true);
          unread.reload();
        }}
      >
        <Bell size={20} aria-hidden="true" />
        {!!unread.data?.count && (
          <i className={s.unreadDot} aria-hidden="true" />
        )}
        <span>通知</span>
      </button>
      <Modal title="通知" open={open} onClose={() => setOpen(false)}>
        <div className={s.dialogBody}>
          {list.loading && <p role="status">正在加载通知…</p>}
          <ErrorBox error={list.error} retry={list.reload} />
          {list.data?.notifications.length === 0 && <p>暂无通知</p>}
          <ul className={s.notificationList}>
            {list.data?.notifications.map((item) => (
              <li key={item.id}>
                {item.is_read ? (
                  <p>{item.content}</p>
                ) : (
                  <button
                    type="button"
                    onClick={() => void markRead(item)}
                    disabled={pending !== null}
                  >
                    <span className={s.notificationUnread} aria-hidden="true" />
                    {item.content}
                  </button>
                )}
                <time dateTime={item.created_at}>
                  {new Date(item.created_at).toLocaleString("zh-CN")}
                </time>
              </li>
            ))}
          </ul>
          <ErrorBox error={failure} />
        </div>
      </Modal>
    </>
  );
}
