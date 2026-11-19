import { ApiError, getJson, postJson } from "../api/client";
import { authSession, sessionClaims } from "../session/auth";
import { CHUNK_SIZE, fileHash, type UploadProgress } from "./upload";
import {
  createOSSTransport,
  OSSError,
  type Grant,
  type UploadKind,
} from "./ossTransport";

import { cancelUpload } from "./directUploadConfig";

type Upload = {
  id: string;
  kind: UploadKind;
  size: number;
  content_type: string;
  status:
    "pending" | "ready" | "published" | "cancelled" | "failed" | "expired";
  expires_at: string;
};
type Authorization = { upload: Upload; grant: Grant };
type Checkpoint = {
  sessionID: string;
  multipartID?: string;
  objectComplete?: boolean;
};
type Send = ReturnType<typeof createOSSTransport>;
type Entry = { checkpoint: Checkpoint; send?: Send };

function checkpointAt(key: string): Checkpoint | undefined {
  try {
    const value: Checkpoint = JSON.parse(localStorage.getItem(key) ?? "null");
    if (value && /^[a-f0-9]{32}$/.test(value.sessionID)) {
      return {
        sessionID: value.sessionID,
        multipartID:
          typeof value.multipartID === "string" ? value.multipartID : undefined,
        objectComplete: value.objectComplete === true,
      };
    }
  } catch {
    // Invalid and unavailable local storage do not prevent uploading.
  }
}

function save(key: string, checkpoint?: Checkpoint) {
  try {
    if (checkpoint) localStorage.setItem(key, JSON.stringify(checkpoint));
    else localStorage.removeItem(key);
  } catch {
    // The current transfer still works without a saved checkpoint.
  }
}

const text = (node: ParentNode | undefined, tag: string) =>
  node?.querySelector(tag)?.textContent || "";
const confirmed = (asset: Upload) =>
  asset.status === "ready" || asset.status === "published";
const escapeXML = (value: string) =>
  value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");

export function createDirectUploadRun(signal: AbortSignal) {
  const revision = authSession.getState().revision;
  const owner = sessionClaims()?.account_id;
  if (!owner) throw new ApiError("请先登录", 401);
  const controller = new AbortController();
  const abort = () => controller.abort(signal.reason);
  signal.addEventListener("abort", abort, { once: true });
  if (signal.aborted) abort();
  const unsubscribe = authSession.subscribe((session) => {
    if (session.revision !== revision)
      controller.abort(new ApiError("会话已改变，请重试", 409));
  });
  const entries = new Map<string, Entry>();
  const api = { authRequired: true, signal: controller.signal };
  const pendingCleanup: Promise<unknown>[] = [];

  function cancel() {
    if (
      controller.signal.reason !== cancelUpload ||
      authSession.getState().revision !== revision
    )
      return;
    for (const [storageKey, entry] of entries) {
      save(storageKey);
      if (
        entry.send &&
        entry.checkpoint.multipartID &&
        !entry.checkpoint.objectComplete
      ) {
        pendingCleanup.push(
          entry
            .send("DELETE", AbortSignal.timeout(5000), {
              uploadId: entry.checkpoint.multipartID,
            })
            .catch(() => undefined),
        );
      }
      pendingCleanup.push(
        postJson(`/uploads/${entry.checkpoint.sessionID}/cancel`, undefined, {
          authRequired: true,
          timeoutMs: 10_000,
        }).catch(() => undefined),
      );
    }
    entries.clear();
  }
  controller.signal.addEventListener("abort", cancel, { once: true });

  async function upload(
    file: File,
    kind: UploadKind,
    progress: (state: UploadProgress) => void,
  ) {
    let uploaded = 0;
    const report = (stage: string) =>
      progress({ uploaded, total: file.size, stage });
    const content_type = kind === "video" ? "video/mp4" : file.type;
    const limit = { video: 200, cover: 10, avatar: 5 }[kind] * 1024 * 1024;
    const allowed =
      kind === "video"
        ? /\.mp4$/i.test(file.name)
        : ["image/jpeg", "image/png", "image/webp"].includes(content_type);
    if (!allowed || !file.size || file.size > limit)
      throw new Error("文件格式或大小不符合直传要求");
    const matches = (asset: Upload) =>
      asset.kind === kind &&
      asset.size === file.size &&
      asset.content_type === content_type;
    report("准备文件");
    const fingerprint = await fileHash(file, controller.signal);
    const storageKey = `feedsystem:oss-upload:v1:${owner}:${kind}:${file.size}:${fingerprint}`;
    let checkpoint = checkpointAt(storageKey);
    let grant: Authorization | undefined;
    if (checkpoint) {
      try {
        const { upload: existing } = await getJson<{ upload: Upload }>(
          `/uploads/${checkpoint.sessionID}`,
          api,
        );
        if (
          existing.id !== checkpoint.sessionID ||
          !matches(existing) ||
          (!confirmed(existing) && existing.status !== "pending")
        ) {
          checkpoint = undefined;
        } else {
          entries.set(storageKey, { checkpoint });
          if (controller.signal.aborted) cancel();
          controller.signal.throwIfAborted();
          if (confirmed(existing)) {
            uploaded = file.size;
            report("校验完成");
            return existing.id;
          }
        }
      } catch (error) {
        if (!(error instanceof ApiError) || ![404, 410].includes(error.status))
          throw error;
        checkpoint = undefined;
      }
    }
    if (!checkpoint) {
      save(storageKey);
      grant = await postJson<Authorization>(
        "/uploads",
        { kind, size: file.size, content_type },
        api,
      );
      checkpoint = { sessionID: grant.upload.id };
    }
    if (!/^[a-f0-9]{32}$/.test(checkpoint.sessionID))
      throw new Error("上传会话无效");
    const entry: Entry = { checkpoint };
    const base = `/uploads/${checkpoint.sessionID}`;
    const persist = () => save(storageKey, entry.checkpoint);
    entries.set(storageKey, entry);
    if (controller.signal.aborted) cancel();
    controller.signal.throwIfAborted();
    persist();

    let refreshing: Promise<Authorization> | undefined;
    let active: Authorization | undefined;
    function installGrant(value: Authorization) {
      if (
        value.upload.id !== entry.checkpoint.sessionID ||
        !matches(value.upload) ||
        value.upload.status !== "pending"
      )
        throw new Error("上传授权与当前任务不匹配");
      entry.send = createOSSTransport(value.grant, kind, value.upload.id);
      active = value;
    }
    if (grant) installGrant(grant);
    async function send(
      method: Parameters<Send>[0],
      queries?: Parameters<Send>[2],
      body?: Parameters<Send>[3],
      mime?: string,
    ) {
      for (let attempt = 1; ; attempt++) {
        controller.signal.throwIfAborted();
        if (
          !active ||
          Date.parse(active.grant.expires_at) <= Date.now() + 30_000
        ) {
          const renewal = (refreshing ??= postJson<Authorization>(
            `${base}/credentials`,
            undefined,
            api,
          ));
          try {
            const next = await renewal;
            controller.signal.throwIfAborted();
            if (active !== next) installGrant(next);
          } finally {
            if (refreshing === renewal) refreshing = undefined;
          }
        }
        const used = active;
        try {
          return await entry.send!(
            method,
            controller.signal,
            queries,
            body,
            mime,
          );
        } catch (error) {
          if (!(error instanceof OSSError) || !error.retryable || attempt === 3)
            throw error;
          if (error.expired && active === used) active = undefined;
        }
      }
    }

    async function validate() {
      report("正在校验文件");
      try {
        const { upload: asset } = await postJson<{ upload: Upload }>(
          `${base}/complete`,
          undefined,
          { ...api, timeoutMs: 110_000 },
        );
        controller.signal.throwIfAborted();
        if (
          asset.id !== entry.checkpoint.sessionID ||
          !matches(asset) ||
          !confirmed(asset)
        )
          throw new Error("后端尚未确认上传资产");
        return asset.id;
      } catch (error) {
        if (error instanceof ApiError && [410, 422].includes(error.status))
          save(storageKey);
        throw error;
      }
    }

    if (entry.checkpoint.objectComplete) {
      uploaded = file.size;
      return validate();
    }
    if (kind !== "video") {
      report(kind === "avatar" ? "上传头像" : "上传封面");
      await send("PUT", undefined, file, content_type);
      uploaded = file.size;
    } else {
      const count = Math.ceil(file.size / CHUNK_SIZE);
      const partSize = (number: number) =>
        Math.min(CHUNK_SIZE, file.size - (number - 1) * CHUNK_SIZE);
      const parts = new Map<number, string>();
      let multipartID = entry.checkpoint.multipartID;
      if (multipartID) {
        try {
          const listed = await send("GET", {
            uploadId: multipartID,
            "max-parts": "1000",
          });
          if (text(listed.xml, "IsTruncated") === "true")
            throw new Error("分片列表超出当前文件范围");
          for (const row of Array.from(
            listed.xml?.querySelectorAll("Part") || [],
          )) {
            const number = Number(text(row, "PartNumber"));
            const etag = text(row, "ETag");
            if (
              Number.isInteger(number) &&
              number >= 1 &&
              number <= count &&
              Number(text(row, "Size")) === partSize(number) &&
              etag
            ) {
              parts.set(number, etag);
            }
          }
        } catch (error) {
          if (!(error instanceof OSSError) || error.code !== "NoSuchUpload")
            throw error;
          try {
            uploaded = file.size;
            return await validate();
          } catch (verification) {
            if (
              !(verification instanceof ApiError) ||
              verification.status !== 409
            )
              throw verification;
            uploaded = 0;
            multipartID = undefined;
          }
        }
      }
      if (!multipartID) {
        const started = await send(
          "POST",
          { uploads: "" },
          undefined,
          content_type,
        );
        multipartID = text(started.xml, "UploadId");
        if (!multipartID) throw new Error("对象存储未返回分片任务");
        entry.checkpoint.multipartID = multipartID;
        controller.signal.throwIfAborted();
        persist();
      }
      for (const number of parts.keys()) uploaded += partSize(number);
      report("直传视频");
      const remaining = Array.from({ length: count }, (_, i) => i + 1).filter(
        (number) => !parts.has(number),
      );
      const uploadId = multipartID;
      let cursor = 0;
      const worker = async () => {
        while (cursor < remaining.length) {
          controller.signal.throwIfAborted();
          const number = remaining[cursor++]!;
          const start = (number - 1) * CHUNK_SIZE;
          const part = file.slice(start, start + partSize(number));
          const { etag } = await send(
            "PUT",
            { uploadId, partNumber: String(number) },
            part,
            "application/octet-stream",
          );
          controller.signal.throwIfAborted();
          if (!etag)
            throw new Error("对象存储未返回分片 ETag，请检查 CORS 配置");
          parts.set(number, etag);
          uploaded += part.size;
          report("直传视频");
        }
      };
      const workers = Array.from(
        { length: Math.min(3, remaining.length) },
        worker,
      );
      try {
        await Promise.all(workers);
      } catch (error) {
        controller.abort(error);
        await Promise.allSettled(workers);
        throw error;
      }
      const partXML = [...parts]
        .sort(([a], [b]) => a - b)
        .map(
          ([number, etag]) =>
            `<Part><PartNumber>${number}</PartNumber><ETag>${escapeXML(etag)}</ETag></Part>`,
        )
        .join("");
      await send(
        "POST",
        { uploadId },
        `<CompleteMultipartUpload>${partXML}</CompleteMultipartUpload>`,
        "application/xml",
      );
    }
    controller.signal.throwIfAborted();
    entry.checkpoint.objectComplete = true;
    persist();
    return validate();
  }

  return {
    upload,
    signal: controller.signal,
    published() {
      for (const key of entries.keys()) save(key);
    },
    async close() {
      signal.removeEventListener("abort", abort);
      unsubscribe();
      controller.signal.removeEventListener("abort", cancel);
      await Promise.allSettled(pendingCleanup);
      entries.clear();
    },
  };
}
