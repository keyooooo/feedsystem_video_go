import { create } from "zustand";
import type { FeedVideoItem } from "../api/types";
import { authSession } from "../session/auth";

type Confirmation = {
  revision: number;
  liked: boolean;
  count: number;
};
export const useVideoLikes = create<Record<number, Confirmation>>(() => ({}));

authSession.subscribe((state, previous) => {
  if (state.revision !== previous.revision) useVideoLikes.setState({}, true);
});

export function confirmVideoLike(item: FeedVideoItem, revision: number) {
  if (authSession.getState().revision !== revision) return;
  const liked = !item.is_liked;
  const confirmation = {
    revision,
    liked,
    count: Math.max(0, item.likes_count + (liked ? 1 : -1)),
  };
  useVideoLikes.setState({ [item.id]: confirmation });
  setTimeout(() => {
    const current = useVideoLikes.getState();
    if (current[item.id] !== confirmation) return;
    const next = { ...current };
    delete next[item.id];
    useVideoLikes.setState(next, true);
  }, 15000);
  return true;
}

export function applyVideoLike(
  item: FeedVideoItem,
  confirmation: Confirmation | undefined,
  revision: number,
) {
  return confirmation?.revision === revision &&
    confirmation.liked !== item.is_liked
    ? { ...item, is_liked: confirmation.liked, likes_count: confirmation.count }
    : item;
}
