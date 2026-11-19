import {
  useEffect,
  useRef,
  useState,
  useCallback,
  type ReactNode,
} from "react";
import { useStore } from "zustand";
import { create } from "zustand";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Film } from "lucide-react";
import { authSession } from "../session/auth";
import { decodeJwtPayload } from "../utils/jwt";
import s from "./App.module.css";
export const useSession = () => useStore(authSession);
export const useIdentity = () => decodeJwtPayload(useSession().token || "");
export const useNotice = create<{
  message: string;
  show: (message: string) => void;
}>((set) => ({ message: "", show: (message) => set({ message }) }));
export function Notices() {
  const { message, show } = useNotice();
  useEffect(() => {
    if (message) {
      const timer = setTimeout(() => show(""), 4000);
      return () => clearTimeout(timer);
    }
  }, [message, show]);
  return message ? (
    <div role="status" className={s.toast}>
      {message}
    </div>
  ) : null;
}
export function Avatar({ name, url }: { name: string; url?: string }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [url]);
  return (
    <span className={s.avatar}>
      {url && !failed ? (
        <img src={url} alt="" onError={() => setFailed(true)} />
      ) : (
        name.slice(0, 1).toUpperCase()
      )}
    </span>
  );
}
export function Empty({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
  return (
    <div className={s.empty}>
      <Film size={40} />
      <h2>{title}</h2>
      {children}
    </div>
  );
}
export function ErrorBox({
  error,
  retry,
}: {
  error: unknown;
  retry?: () => void;
}) {
  return error ? (
    <div className={s.error} role="alert">
      {error instanceof Error ? error.message : String(error)}{" "}
      {retry && <button onClick={retry}>重试</button>}
    </div>
  ) : null;
}
export function Modal({
  title,
  open,
  onClose,
  children,
  inline = false,
}: {
  inline?: boolean;
  title: string;
  open: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  if (inline)
    return open ? (
      <aside
        data-inline-comments
        className={`${s.dialog} ${s.inlineComments}`}
        aria-label={title}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
        }}
      >
        <div className={s.topline}>
          <h2>{title}</h2>
          <button aria-label="关闭评论" onClick={onClose}>
            <X size={20} />
          </button>
        </div>
        {children}
      </aside>
    ) : null;
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className={s.overlay} />
        <Dialog.Content className={s.dialog} aria-describedby={undefined}>
          <div className={s.topline}>
            <Dialog.Title>{title}</Dialog.Title>
            <Dialog.Close asChild>
              <button aria-label="关闭">
                <X size={20} />
              </button>
            </Dialog.Close>
          </div>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function useLoad<T>(key: string, load: () => Promise<T>) {
  const fn = useRef(load);
  fn.current = load;
  const [retry, setRetry] = useState(0);
  const [state, setState] = useState<{
    key: string;
    data: T | null;
    error: unknown;
    loading: boolean;
  }>({ key, data: null, error: null, loading: true });
  useEffect(() => {
    let active = true;
    setState({ key, data: null, error: null, loading: true });
    fn.current()
      .then((data) => {
        if (active) setState({ key, data, error: null, loading: false });
      })
      .catch((error) => {
        if (active) setState({ key, data: null, error, loading: false });
      });
    return () => {
      active = false;
    };
  }, [key, retry]);
  const reload = useCallback(() => setRetry((v) => v + 1), []);
  return {
    ...(state.key === key
      ? state
      : { key, data: null, error: null, loading: true }),
    reload,
  };
}

export function useMedia(query: string) {
  const [matches, setMatches] = useState(
    () => typeof window !== "undefined" && window.matchMedia(query).matches,
  );
  useEffect(() => {
    const media = window.matchMedia(query);
    const update = () => setMatches(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [query]);
  return matches;
}
