import { authSession } from "../session/auth";
export class ApiError extends Error {
  status: number;
  payload?: unknown;
  constructor(message: string, status: number, payload?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.payload = payload;
  }
}
const base = (import.meta.env.VITE_API_BASE as string | undefined) ?? "/api";
let pending: { revision: number; promise: Promise<string | null> } | null =
  null;
async function refresh() {
  const session = authSession.getState();
  if (!session.refreshToken) return null;
  if (pending?.revision === session.revision) return pending.promise;
  const revision = session.revision;
  const promise = (async () => {
    try {
      const res = await fetch(`${base}/account/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: session.refreshToken }),
      });
      if (!res.ok) throw new Error("登录已过期");
      const data = await res.json();
      if (authSession.getState().revision !== revision) return null;
      if (typeof data.token !== "string" || !data.token)
        throw new Error("无效的登录信息");
      authSession.getState().setToken(data.token);
      return data.token as string;
    } catch {
      if (authSession.getState().revision === revision)
        authSession.getState().clearTokens();
      return null;
    } finally {
      if (pending?.revision === revision) pending = null;
    }
  })();
  pending = { revision, promise };
  return promise;
}
type Options = { authRequired?: boolean; signal?: AbortSignal };
async function request<T>(
  path: string,
  body: unknown,
  form: boolean,
  options?: Options,
): Promise<T> {
  const session = authSession.getState(),
    revision = session.revision;
  if (options?.authRequired && !session.token)
    throw new ApiError("请先登录", 401);
  const headers: Record<string, string> = form
    ? {}
    : { "Content-Type": "application/json" };
  if (session.token) headers.Authorization = `Bearer ${session.token}`;
  const send = () =>
    fetch(`${base}${path}`, {
      method: "POST",
      headers,
      body: form ? (body as FormData) : JSON.stringify(body ?? {}),
      signal: options?.signal,
    });
  let res = await send();
  if (
    res.status === 401 &&
    path !== "/account/refresh" &&
    authSession.getState().revision === revision
  ) {
    const token = await refresh();
    if (token) {
      headers.Authorization = `Bearer ${token}`;
      res = await send();
    }
  }
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (authSession.getState().revision !== revision)
    throw new ApiError("会话已改变，请重试", 409);
  if (!res.ok) {
    if (res.status === 401) authSession.getState().clearTokens();
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
