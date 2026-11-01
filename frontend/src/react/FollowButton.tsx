import { useEffect } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import { useSession, useIdentity, useNotice } from "./ui";
import { loadFollowing, toggleFollowing, useSocialState } from "./social-state";
export default function FollowButton({
  id,
  onChanged,
}: {
  id: number;
  onChanged?: () => void;
}) {
  const session = useSession(),
    identity = useIdentity(),
    state = useSocialState(),
    navigate = useNavigate(),
    location = useLocation(),
    show = useNotice((s) => s.show);
  useEffect(() => {
    void loadFollowing();
  }, [session.revision]);
  if (identity?.account_id === id) return null;
  const followed = state.ids.includes(id);
  return (
    <button
      disabled={state.loading || state.busy.includes(id)}
      aria-pressed={followed}
      onClick={async () => {
        if (!session.token) {
          navigate(
            "/account?redirect=" +
              encodeURIComponent(location.pathname + location.search),
          );
          return;
        }
        try {
          await toggleFollowing(id);
          show(followed ? "已取消关注" : "关注成功");
          onChanged?.();
        } catch (error) {
          show(error instanceof Error ? error.message : "关注失败");
        }
      }}
    >
      {followed ? "已关注" : "关注"}
    </button>
  );
}
