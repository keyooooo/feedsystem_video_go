import { create } from "zustand";
import { authSession } from "../session/auth";
import * as api from "../api/social";
type SocialState = {
  revision: number;
  ids: number[];
  loading: boolean;
  busy: number[];
  error: unknown;
};
export const useSocialState = create<SocialState>(() => ({
  revision: -1,
  ids: [],
  loading: false,
  busy: [],
  error: null,
}));
let pending: Promise<void> | null = null;
export function loadFollowing() {
  const session = authSession.getState();
  const state = useSocialState.getState();
  if (state.revision === session.revision) return pending ?? Promise.resolve();
  const revision = session.revision;
  useSocialState.setState({
    revision,
    ids: [],
    loading: !!session.token,
    busy: [],
    error: null,
  });
  if (!session.token) {
    pending = null;
    return Promise.resolve();
  }
  const task = api
    .getAllVloggers()
    .then((result) => {
      if (authSession.getState().revision === revision)
        useSocialState.setState({
          ids: result.vloggers.map((a) => a.id),
          loading: false,
        });
    })
    .catch((error) => {
      if (authSession.getState().revision === revision)
        useSocialState.setState({ error, loading: false });
    });
  pending = task;
  return task;
}
export async function toggleFollowing(id: number) {
  await loadFollowing();
  const revision = authSession.getState().revision,
    state = useSocialState.getState();
  if (!authSession.getState().token) throw new Error("请先登录");
  if (state.busy.includes(id)) return;
  const following = state.ids.includes(id);
  useSocialState.setState({ busy: [...state.busy, id] });
  try {
    if (following) await api.unfollow(id);
    else await api.follow(id);
    if (authSession.getState().revision === revision)
      useSocialState.setState((s) => ({
        ids: following
          ? s.ids.filter((value) => value !== id)
          : [...new Set([...s.ids, id])],
      }));
  } finally {
    if (authSession.getState().revision === revision)
      useSocialState.setState((s) => ({
        busy: s.busy.filter((value) => value !== id),
      }));
  }
}
authSession.subscribe((state, previous) => {
  if (state.revision !== previous.revision) {
    useSocialState.setState({
      revision: -1,
      ids: [],
      busy: [],
      loading: false,
      error: null,
    });
    pending = null;
  }
});
