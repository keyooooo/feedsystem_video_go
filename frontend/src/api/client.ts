import {
  authSession,
  cookieResumeAllowed,
  secureSessions,
  withCookieLock,
} from "../session/auth";
import { decodeJwtPayload } from "../utils/jwt";
export class ApiError extends Error {
  constructor(message: string, status: number, payload?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.payload = payload;
  }
  status: number;
  payload?: unknown;
}
const base = (import.meta.env.VITE_API_BASE as string | undefined) ?? "/api";
let pending: { revision: number; promise: Promise<string | null> } | null =
  null;
type Options = {
  authRequired?: boolean;
  signal?: AbortSignal;
  timeoutMs?: number;
};
async function boundedFetch(url: string, init: RequestInit, timeoutMs: number) {
  const controller = new AbortController();
  const abort = () => controller.abort(init.signal?.reason);
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  init.signal?.addEventListener("abort", abort, { once: true });
  if (init.signal?.aborted) abort();
  try {
    const response = await fetch(url, {
      ...init,
      credentials: "same-origin",
      signal: controller.signal,
    });
    // Keep the deadline active while reading the body, not just until headers arrive.
    const text = await response.text();
    return { response, text };
  } catch (error) {
    if (timedOut && !init.signal?.aborted)
      throw new ApiError("请求超时，请稍后重试", 408);
    throw error;
  } finally {
    clearTimeout(timer);
    init.signal?.removeEventListener("abort", abort);
  }
}
async function refresh() {
  const session = authSession.getState();
  if (secureSessions ? !cookieResumeAllowed() : !session.refreshToken)
    return null;
  if (pending?.revision === session.revision) return pending.promise;
  const revision = session.revision;
  const promise = withCookieLock(async () => {
    try {
      if (
        authSession.getState().revision !== revision ||
        (secureSessions && !cookieResumeAllowed())
      )
        return null;
      const { response: res, text } = await boundedFetch(
        `${base}/account/refresh`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            secureSessions ? {} : { refresh_token: session.refreshToken },
          ),
        },
        10000,
      );
      if (authSession.getState().revision !== revision) return null;
      if (!res.ok) {
        if ([400, 401, 403].includes(res.status)) {
          authSession.getState().clearTokens();
          throw new ApiError("登录已过期，请重新登录", 401);
        }
        throw new ApiError("登录续期暂不可用，请稍后重试", res.status);
      }
      let data: { token?: unknown };
      try {
        data = JSON.parse(text);
      } catch {
        throw new ApiError("登录续期响应无效，请稍后重试", 502);
      }
      if (authSession.getState().revision !== revision) return null;
      if (!data || typeof data.token !== "string" || !data.token)
        throw new ApiError("登录续期响应无效，请稍后重试", 502);
      const previous = decodeJwtPayload(session.token ?? "")?.account_id;
      const next = decodeJwtPayload(data.token)?.account_id;
      if (secureSessions && previous && previous !== next) {
        authSession.getState().setTokens(data.token, "");
        return null;
      }
      authSession.getState().setToken(data.token);
      return data.token;
    } finally {
      if (pending?.revision === revision) pending = null;
    }
  });
  pending = { revision, promise };
  return promise;
}
export async function resumeCookieSession() {
  if (!secureSessions || !cookieResumeAllowed()) return;
  await refresh().catch(() => null);
}

async function request<T>(
  path: string,
  body: unknown,
  form: boolean,
  options?: Options,
  method = "POST",
): Promise<T> {
  const session = authSession.getState(),
    revision = session.revision;
  if (options?.authRequired && !session.token)
    throw new ApiError("请先登录", 401);
  const headers: Record<string, string> =
    form || method === "GET" ? {} : { "Content-Type": "application/json" };
  let sentToken = session.token;
  if (sentToken) headers.Authorization = `Bearer ${sentToken}`;
  const send = () =>
    boundedFetch(
      `${base}${path}`,
      {
        method,
        headers,
        body:
          method === "GET"
            ? undefined
            : form
              ? (body as FormData)
              : JSON.stringify(body ?? {}),
        signal: options?.signal,
      },
      options?.timeoutMs ?? (form ? 120000 : 30000),
    );
  let { response: res, text } = await send();
  const cookieAction =
    secureSessions &&
    ["/account/login", "/account/logout", "/account/changePassword"].includes(
      path,
    );
  if (res.status === 401 && path !== "/account/refresh" && !cookieAction) {
    if (authSession.getState().revision !== revision)
      throw new ApiError("会话已改变，请重试", 409);
    options?.signal?.throwIfAborted();
    const current = authSession.getState().token;
    const token = current && current !== sentToken ? current : await refresh();
    if (token) {
      options?.signal?.throwIfAborted();
      if (authSession.getState().revision !== revision)
        throw new ApiError("会话已改变，请重试", 409);
      sentToken = token;
      headers.Authorization = `Bearer ${token}`;
      ({ response: res, text } = await send());
    }
  }
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (authSession.getState().revision !== revision)
    throw new ApiError("会话已改变，请重试", 409);
  if (!res.ok) {
    if (
      res.status === 401 &&
      !cookieAction &&
      authSession.getState().token === sentToken
    )
      authSession.getState().clearTokens();
    throw new ApiError(
      typeof data === "object" && data && "error" in data
        ? String(data.error)
        : `请求失败 (${res.status})`,
      res.status,
      data,
    );
  }
  return data as T;
}
export const postJson = <T>(path: string, body: unknown, options?: Options) =>
  request<T>(path, body, false, options);
export const postForm = <T>(path: string, body: FormData, options?: Options) =>
  request<T>(path, body, true, options);

export const getJson = <T>(path: string, options?: Options) =>
  request<T>(path, undefined, false, options, "GET");

// Fetch keeps the access token in the Authorization header in both session modes.
export function openNotificationStream(token: string, signal: AbortSignal) {
  return fetch(`${base}/notification/stream`, {
    headers: { Accept: "text/event-stream", Authorization: `Bearer ${token}` },
    credentials: "same-origin",
    signal,
  });
}
