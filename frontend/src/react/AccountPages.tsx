import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Eye, EyeOff, LogOut } from "lucide-react";
import * as account from "../api/account";
import { postJson } from "../api/client";
import { cancelUpload, directUploadEnabled } from "./directUploadConfig";
import type { createDirectUploadRun } from "./directUpload";
import type { UploadProgress } from "./upload";
import { Brand } from "./Brand";
import {
  authSession,
  markExplicitLogout,
  secureSessions,
} from "../session/auth";
import {
  useSession,
  useIdentity,
  useNotice,
  useLoad,
  ErrorBox,
  Modal,
} from "./ui";
import s from "./App.module.css";
export function safeRedirect(value: string | null) {
  if (
    !value?.startsWith("/") ||
    value.startsWith("//") ||
    value.includes("\\") ||
    [...value].some((c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127)
  )
    return "/";
  try {
    return new URL(value, "https://shortvideo.invalid").origin ===
      "https://shortvideo.invalid"
      ? value
      : "/";
  } catch {
    return "/";
  }
}
export function AuthPage({
  register = false,
  password = false,
}: {
  register?: boolean;
  password?: boolean;
}) {
  const navigate = useNavigate(),
    [query] = useSearchParams(),
    show = useNotice((v) => v.show);
  const [visible, setVisible] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget),
      username = String(data.get("username")).trim(),
      pass = String(data.get("password"));
    setBusy(true);
    setError(null);
    try {
      if (password) {
        await account.changePassword(
          username,
          pass,
          String(data.get("newPassword")),
        );
        show("密码已更新");
        navigate("/account");
      } else if (register) {
        await account.register(username, pass);
        show("注册成功，请登录");
        navigate("/account");
      } else {
        const result = await account.login(username, pass);
        if (!secureSessions && !result.refresh_token)
          throw new Error("登录响应缺少续期信息");
        authSession
          .getState()
          .setTokens(result.token, result.refresh_token ?? "");
        navigate(safeRedirect(query.get("redirect")), { replace: true });
      }
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className={`${s.panel} ${s.auth}`}>
      <div className={`${s.brand} ${s.authBrand}`}>
        <Brand />
      </div>
      <h1>{password ? "修改密码" : register ? "创建你的账号" : "欢迎回来"}</h1>
      <p className={s.muted}>
        {register
          ? "记录生活，分享你眼中的精彩。"
          : "登录后，继续发现你喜欢的内容。"}
      </p>
      <form onSubmit={submit}>
        <label>
          用户名
          <input name="username" autoComplete="username" required />
        </label>
        <label>
          {password ? "原密码" : "密码"}
          <div className={s.row}>
            <input
              style={{ flex: 1, minWidth: 0 }}
              name="password"
              type={visible ? "text" : "password"}
              autoComplete={register ? "new-password" : "current-password"}
              required
            />
            <button
              type="button"
              aria-label={visible ? "隐藏密码" : "显示密码"}
              onClick={() => setVisible(!visible)}
            >
              {visible ? <EyeOff /> : <Eye />}
            </button>
          </div>
        </label>
        {password && (
          <label>
            新密码
            <input
              name="newPassword"
              type="password"
              autoComplete="new-password"
              required
            />
          </label>
        )}
        <ErrorBox error={error} />
        <button className={s.primary} disabled={busy}>
          {busy
            ? "请稍候…"
            : password
              ? "保存新密码"
              : register
                ? "创建账号"
                : "登录"}
        </button>
      </form>
      <p style={{ marginTop: 24 }}>
        <Link to={register ? "/account" : "/account/register"}>
          {register ? "已有账号？立即登录" : "还没有账号？立即注册"}
        </Link>
      </p>
    </section>
  );
}
export function Settings() {
  const session = useSession(),
    identity = useIdentity(),
    navigate = useNavigate(),
    show = useNotice((v) => v.show);
  const profile = useLoad(String(identity?.account_id), () =>
    account.findById(Number(identity?.account_id)),
  );
  const [error, setError] = useState<unknown>(null),
    [busy, setBusy] = useState(false),
    [confirm, setConfirm] = useState(false),
    [loggingOut, setLoggingOut] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const [progress, setProgress] = useState<UploadProgress | null>(null);
  const [applying, setApplying] = useState(false);
  useEffect(() => () => controller.current?.abort(), []);

  async function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (controller.current || profile.loading || !profile.data) return;
    const form = new FormData(e.currentTarget);
    const avatar = form.get("avatar");
    const limit = directUploadEnabled ? 5 : 10;
    if (
      avatar instanceof File &&
      avatar.size &&
      (!["image/jpeg", "image/png", "image/webp"].includes(avatar.type) ||
        avatar.size > limit * 1024 * 1024)
    ) {
      setError(new Error(`头像请选择 ${limit}MB 以内的 JPG、PNG 或 WebP 图片`));
      return;
    }
    const task = new AbortController();
    controller.current = task;
    setBusy(true);
    setError(null);
    let run: ReturnType<typeof createDirectUploadRun> | undefined;
    try {
      let assetID: string | undefined;
      let avatar_url: string | undefined;
      if (avatar instanceof File && avatar.size) {
        if (directUploadEnabled) {
          const direct = await import("./directUpload");
          task.signal.throwIfAborted();
          run = direct.createDirectUploadRun(task.signal);
          assetID = await run.upload(avatar, "avatar", setProgress);
        } else {
          avatar_url = (await account.uploadAvatar(avatar)).avatar_url;
        }
      }
      const signal = run?.signal ?? task.signal;
      signal.throwIfAborted();
      setApplying(true);
      const name = String(form.get("username")).trim();
      if (name && name !== identity?.username) {
        const data = await account.rename(name);
        signal.throwIfAborted();
        session.setToken(data.token);
      }
      if (assetID) {
        await postJson(
          "/account/applyAvatar",
          { asset_id: assetID },
          { authRequired: true, signal },
        );
        signal.throwIfAborted();
        run?.published();
      }
      await postJson(
        "/account/updateProfile",
        { bio: String(form.get("bio")), avatar_url },
        { authRequired: true, signal },
      );
      signal.throwIfAborted();
      show("个人资料已保存");
    } catch (e) {
      if (task.signal.reason === cancelUpload) show("头像上传已取消");
      else if (!task.signal.aborted) setError(e);
    } finally {
      await run?.close();
      controller.current = null;
      setProgress(null);
      setApplying(false);
      setBusy(false);
    }
  }
  async function logout() {
    if (loggingOut) return;
    setLoggingOut(true);
    markExplicitLogout();
    try {
      await account.logout();
      show("已退出登录");
    } catch {
      show("已清除此设备的登录状态；服务器暂不可用");
    } finally {
      session.clearTokens();
      setLoggingOut(false);
      navigate("/account");
    }
  }
  return (
    <>
      <p className={s.eyebrow}>YOUR SPACE</p>
      <h1>个人设置</h1>
      <section className={s.panel} style={{ maxWidth: 680 }}>
        <h2>个人资料</h2>
        <form onSubmit={save}>
          <label>
            用户名
            <input
              name="username"
              defaultValue={String(identity?.username || "")}
              required
            />
          </label>
          <label>
            个人简介
            <textarea
              name="bio"
              placeholder="介绍一下自己"
              defaultValue={profile.data?.bio || ""}
            />
          </label>
          <label>
            上传头像
            <input
              name="avatar"
              type="file"
              accept="image/jpeg,image/png,image/webp"
            />
          </label>
          {progress && !applying && (
            <div role="status">
              <p>{progress.stage}</p>
              <progress max={progress.total} value={progress.uploaded} />
              <button
                type="button"
                onClick={() => controller.current?.abort(cancelUpload)}
              >
                取消头像上传
              </button>
            </div>
          )}
          <ErrorBox error={profile.error} retry={profile.reload} />
          <ErrorBox error={error} />
          <button
            className={s.primary}
            disabled={busy || loggingOut || profile.loading || !profile.data}
          >
            {busy ? "保存中…" : "保存资料"}
          </button>
        </form>
        <hr style={{ margin: "32px 0", borderColor: "#303642" }} />
        <h2>账号管理</h2>
        <div className={s.row}>
          <Link to="/account/change-password">修改密码 ↗</Link>
          <button onClick={() => setConfirm(true)}>
            <LogOut size={18} /> 退出登录
          </button>
        </div>
      </section>
      <Modal
        title="退出登录？"
        open={confirm}
        onClose={() => setConfirm(false)}
      >
        <p>下次回来，你可以继续使用当前账号登录。</p>
        <button onClick={logout} disabled={loggingOut}>
          {loggingOut ? "退出中…" : "确认退出"}
        </button>
      </Modal>
    </>
  );
}
