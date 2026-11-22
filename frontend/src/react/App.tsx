import {
  Link,
  Routes,
  Route,
  Navigate,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { Avatar, useIdentity, useLoad, useSession, Notices } from "./ui";
import { lazy, Suspense, type ReactNode } from "react";
import {
  Play,
  Flame,
  ListOrdered,
  Plus,
  MessageCircle,
  UserRound,
  Search,
} from "lucide-react";
import { findById } from "../api/account";
import { Brand, brandName } from "./Brand";
import { NotificationButton } from "./NotificationButton";
import s from "./App.module.css";

const UploadPage = lazy(() => import("./UploadPage"));
const ProfilePage = lazy(() =>
  import("./SocialPages").then((m) => ({ default: m.ProfilePage })),
);
const MessagesPage = lazy(() =>
  import("./SocialPages").then((m) => ({ default: m.MessagesPage })),
);
const FeedPage = lazy(() =>
  import("./VideoPages").then((m) => ({ default: m.FeedPage })),
);
const DetailPage = lazy(() =>
  import("./VideoPages").then((m) => ({ default: m.DetailPage })),
);
const AuthPage = lazy(() =>
  import("./AccountPages").then((m) => ({ default: m.AuthPage })),
);
const Settings = lazy(() =>
  import("./AccountPages").then((m) => ({ default: m.Settings })),
);
const destinations = [
  { Icon: Play, url: "/", label: "推荐" },
  { Icon: Flame, url: "/?feed=hot", label: "热榜" },
  { Icon: ListOrdered, url: "/hot", label: "榜单" },
  { Icon: UserRound, url: "/?feed=following", label: "关注" },
] as const;
function Protected({ children }: { children: ReactNode }) {
  const session = useSession(),
    location = useLocation();
  return session.token ? (
    children
  ) : (
    <Navigate
      to={
        "/account?redirect=" +
        encodeURIComponent(location.pathname + location.search)
      }
      replace
    />
  );
}
export default function App() {
  const navigate = useNavigate(),
    session = useSession(),
    location = useLocation(),
    identity = useIdentity();
  const query = new URLSearchParams(location.search);
  const home = location.pathname === "/";
  const following = home && query.get("feed") === "following";
  const hot = home && query.get("feed") === "hot";
  const activeDestination = home
    ? following
      ? "/?feed=following"
      : hot
        ? "/?feed=hot"
        : "/"
    : location.pathname;
  const accountId = identity?.account_id;
  const account = useLoad(
    `header-account:${accountId ?? ""}:${session.revision}`,
    () => (accountId ? findById(accountId) : Promise.resolve(null)),
  );
  const signIn =
    "/account?redirect=" +
    encodeURIComponent(location.pathname + location.search);
  return (
    <div className={s.shell}>
      <aside className={s.sidebar}>
        <Link className={s.brand} to="/" aria-label={brandName}>
          <Brand />
        </Link>
        <nav className={s.nav} aria-label="主导航">
          {destinations.map(({ Icon, url, label }) => (
            <Link
              key={url}
              to={url}
              aria-label={label}
              aria-current={activeDestination === url ? "page" : undefined}
            >
              <Icon aria-hidden="true" />
              <span>{label}</span>
            </Link>
          ))}
        </nav>
      </aside>
      <main className={s.main}>
        <header className={s.header}>
          <Link
            className={`${s.brand} ${s.mobileBrand}`}
            to="/"
            aria-label={brandName}
          >
            <Brand />
          </Link>
          <form
            className={s.search}
            onSubmit={(e) => {
              e.preventDefault();
              const keyword = String(
                new FormData(e.currentTarget).get("q") ?? "",
              ).trim();
              const params = new URLSearchParams();
              if (following) params.set("feed", "following");
              if (hot) params.set("feed", "hot");
              if (keyword) params.set("q", keyword);
              navigate({ pathname: "/", search: params.toString() });
            }}
          >
            <input
              key={location.key}
              name="q"
              type="search"
              defaultValue={home ? (query.get("q") ?? "") : ""}
              aria-label="搜索已加载的视频"
              placeholder="搜索"
            />
            <button aria-label="搜索">
              <Search size={18} aria-hidden="true" /> <span>搜索</span>
            </button>
          </form>
          <div className={s.headerActions}>
            <NotificationButton signIn={signIn} />
            <Link to="/messages" className={s.headerAction} aria-label="消息">
              <MessageCircle size={20} aria-hidden="true" />
              <span>消息</span>
            </Link>
            <Link to="/video" className={s.publishLink} aria-label="发布">
              <Plus size={18} aria-hidden="true" />
              <span>发布</span>
            </Link>
            {session.token ? (
              <Link to="/account" className={s.accountLink} aria-label="我的">
                <Avatar
                  name={account.data?.username ?? identity?.username ?? "我"}
                  url={account.data?.avatar_url}
                />
              </Link>
            ) : (
              <Link to={signIn} className={s.signInLink}>
                登录
              </Link>
            )}
          </div>
        </header>
        <div
          className={
            home || /^\/video\/[^/]+$/.test(location.pathname)
              ? `${s.content} ${s.homeContent}`
              : s.content
          }
        >
          <Suspense
            fallback={
              <div
                className={s.skeleton}
                role="status"
                aria-label="正在加载页面"
              />
            }
          >
            <Routes>
              <Route
                path="/video"
                element={
                  <Protected>
                    <UploadPage />
                  </Protected>
                }
              />
              <Route
                path="/"
                element={
                  following ? (
                    <Protected>
                      <FeedPage />
                    </Protected>
                  ) : (
                    <FeedPage />
                  )
                }
              />
              <Route path="/feed" element={<Navigate to="/" replace />} />
              <Route path="/hot" element={<FeedPage key="ranking" ranking />} />
              <Route path="/video/:id" element={<DetailPage />} />
              <Route
                path="/account"
                element={
                  session.token ? <ProfilePage key="own" own /> : <AuthPage />
                }
              />
              <Route path="/u/:id" element={<ProfilePage key="public" />} />
              <Route
                path="/messages/:peerId?"
                element={
                  <Protected>
                    <MessagesPage />
                  </Protected>
                }
              />
              <Route path="/account/register" element={<AuthPage register />} />
              <Route
                path="/account/change-password"
                element={<AuthPage password />}
              />
              <Route
                path="/settings"
                element={
                  <Protected>
                    <Settings />
                  </Protected>
                }
              />
              <Route
                path="*"
                element={
                  <>
                    <p className={s.eyebrow}>404</p>
                    <h1>这个页面暂时找不到</h1>
                    <Link to="/">返回推荐</Link>
                  </>
                }
              />
            </Routes>
          </Suspense>
        </div>
      </main>
      <Notices />
    </div>
  );
}
