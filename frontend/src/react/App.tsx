import {
  NavLink,
  Link,
  Routes,
  Route,
  Navigate,
  useLocation,
  useNavigate,
} from "react-router-dom";
import { useSession, Notices } from "./ui";
import { lazy, Suspense, type ReactNode } from "react";
import {
  Play,
  House,
  Flame,
  PlusCircle,
  MessageCircle,
  User,
  Search,
} from "lucide-react";
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
    session = useSession();
  return (
    <div className={s.shell}>
      <aside className={s.sidebar}>
        <Link className={s.brand} to="/">
          <Play />
          <span>ShortVideo</span>
        </Link>
        <nav className={s.nav} aria-label="主导航">
          {[
            [House, "/", "推荐"],
            [Flame, "/hot", "热榜"],
            [PlusCircle, "/video", "发布"],
            [MessageCircle, "/messages", "私信"],
            [User, "/account", "我的"],
          ].map(([Icon, url, label]) => {
            const C = Icon as typeof House;
            return (
              <NavLink
                end={url === "/" || url === "/video"}
                key={String(url)}
                to={String(url)}
              >
                <C />
                <span>{String(label)}</span>
              </NavLink>
            );
          })}
        </nav>
        <footer>
          发现值得停留的瞬间。
          <br />
          ShortVideo © 2026
        </footer>
      </aside>
      <main className={s.main}>
        <header className={s.header}>
          <form
            className={s.search}
            onSubmit={(e) => {
              e.preventDefault();
              const q = new FormData(e.currentTarget).get("q");
              navigate("/?q=" + encodeURIComponent(String(q || "")));
            }}
          >
            {" "}
            <input
              name="q"
              aria-label="搜索已加载的视频"
              placeholder="搜索已加载的视频"
            />
            <button aria-label="搜索">
              <Search size={20} />
            </button>
          </form>
          <Link to="/video">分享你的创作 ↗</Link>
        </header>
        <div className={s.content}>
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
              <Route path="/" element={<FeedPage key="recommend" />} />
              <Route path="/feed" element={<Navigate to="/" replace />} />
              <Route path="/hot" element={<FeedPage key="hot" hot />} />
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
                    <p className={s.muted}>返回推荐页，继续发现精彩内容。</p>
                    <Link to="/">返回推荐页 ↗</Link>
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
