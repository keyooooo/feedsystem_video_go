import { createStore } from "zustand/vanilla";
import { decodeJwtPayload } from "../utils/jwt";
export const secureSessions = import.meta.env.VITE_SECURE_SESSIONS === "true";
const logoutFlag = "feedsystem:session-logged-out";

function read(key: string) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}
function persist(key: string, value: string | null) {
  try {
    if (value) localStorage.setItem(key, value);
    else localStorage.removeItem(key);
  } catch {
    /* storage may be unavailable */
  }
}
export async function withCookieLock<T>(task: () => Promise<T>): Promise<T> {
  if (secureSessions && typeof navigator !== "undefined" && navigator.locks)
    return await navigator.locks.request("feedsystem-cookie-session", task);
  return task();
}

export function cookieResumeAllowed() {
  return !read(logoutFlag);
}

export function markExplicitLogout() {
  if (secureSessions) {
    persist(logoutFlag, "1");
    authSession.getState().clearTokens();
  }
}

// Remove the legacy credentials when opening a cookie-mode build.
if (secureSessions) {
  persist("access_token", null);
  persist("refresh_token", null);
}

export const authSession = createStore<{
  token: string | null;
  refreshToken: string | null;
  revision: number;
  setToken: (token: string) => void;
  setTokens: (token: string, refresh: string) => void;
  clearTokens: () => void;
}>()((set) => ({
  token: secureSessions ? null : read("access_token"),
  refreshToken: secureSessions ? null : read("refresh_token"),
  revision: 0,
  setToken: (token) => {
    if (!secureSessions) persist("access_token", token);
    set({ token });
  },
  setTokens: (token, refreshToken) => {
    if (secureSessions) persist(logoutFlag, null);
    else {
      persist("access_token", token);
      persist("refresh_token", refreshToken);
    }
    set((s) => ({
      token,
      refreshToken: secureSessions ? null : refreshToken,
      revision: s.revision + 1,
    }));
  },
  clearTokens: () => {
    persist("access_token", null);
    persist("refresh_token", null);
    set((s) => ({ token: null, refreshToken: null, revision: s.revision + 1 }));
  },
}));
export const sessionClaims = () =>
  decodeJwtPayload(authSession.getState().token || "");

if (secureSessions && typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.key === logoutFlag && event.newValue)
      authSession.getState().clearTokens();
  });
}
