import OSS from "ali-oss";

export type UploadKind = "video" | "cover" | "avatar";
export type Grant = {
  bucket: string;
  region: string;
  endpoint: string;
  key: string;
  access_key_id: string;
  access_key_secret: string;
  security_token: string;
  expires_at: string;
};

export class OSSError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code = "") {
    super(status ? `对象存储请求失败 (${status})` : "对象存储网络请求失败");
    this.status = status;
    this.code = code;
  }

  get expired() {
    return [
      "SecurityTokenExpired",
      "InvalidSecurityToken",
      "InvalidAccessKeyId",
      "RequestExpired",
    ].includes(this.code);
  }

  get retryable() {
    return (
      this.expired || [0, 408, 429].includes(this.status) || this.status >= 500
    );
  }
}

export function createOSSTransport(grant: Grant, kind: UploadKind, id: string) {
  const defaultEndpoint = `https://oss-${grant.region}.aliyuncs.com`;
  const cname = grant.endpoint !== defaultEndpoint;
  const origin = cname
    ? grant.endpoint
    : `https://${grant.bucket}.oss-${grant.region}.aliyuncs.com`;
  if (
    !/^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$/.test(grant.bucket) ||
    !/^[a-z0-9]+(?:-[a-z0-9]+)+$/.test(grant.region) ||
    !/^[a-f0-9]{32}$/.test(id) ||
    grant.key !== `direct-upload/staging/${kind}/${id}` ||
    !/^https:\/\/[a-z0-9.-]+$/.test(origin) ||
    origin.includes("-internal.") ||
    !grant.access_key_id ||
    !grant.access_key_secret ||
    !grant.security_token
  )
    throw new Error("对象存储授权参数无效");
  if (import.meta.env.VITE_OSS_UPLOAD_ORIGIN !== origin)
    throw new Error("对象存储地址与站点配置不匹配");
  const sdk = new OSS({
    bucket: grant.bucket,
    cname,
    region: `oss-${grant.region}`,
    endpoint: grant.endpoint,
    secure: true,
    authorizationV4: true,
    accessKeyId: grant.access_key_id,
    accessKeySecret: grant.access_key_secret,
    stsToken: grant.security_token,
  });

  return async function request(
    method: "GET" | "POST" | "PUT" | "DELETE",
    signal: AbortSignal,
    queries?: Record<string, string>,
    body?: Blob | string,
    contentType?: string,
  ) {
    signal.throwIfAborted();
    const remaining = Math.floor(
      (Date.parse(grant.expires_at) - Date.now()) / 1000,
    );
    if (!Number.isFinite(remaining) || remaining <= 0)
      throw new OSSError(403, "SecurityTokenExpired");
    const headers = contentType ? { "content-type": contentType } : undefined;
    // OSS signs flags like uploads as bare keys. The SDK uses null for
    // that canonical form and includes an equals sign for an empty string.
    const signingQueries = queries && Object.fromEntries(
      Object.entries(queries).map(([key, value]) => [key, value === "" ? null : value]),
    );
    const url = await sdk.signatureUrlV4(
      method,
      Math.min(remaining, 300),
      { headers, queries: signingQueries },
      grant.key,
    );
    if (new URL(url).origin !== origin) throw new Error("对象存储签名地址无效");
    const deadline = AbortSignal.any([signal, AbortSignal.timeout(120_000)]);
    try {
      const response = await fetch(url, {
        method,
        headers,
        body,
        signal: deadline,
        credentials: "omit",
        redirect: "error",
        referrerPolicy: "no-referrer",
      });
      const xml =
        (method !== "PUT" && method !== "DELETE") || !response.ok
          ? new DOMParser().parseFromString(
              await response.text(),
              "application/xml",
            )
          : undefined;
      if (!response.ok || xml?.documentElement.localName === "Error") {
        throw new OSSError(
          response.ok ? 502 : response.status,
          xml?.getElementsByTagName("Code")[0]?.textContent || "",
        );
      }
      return { xml, etag: response.headers.get("ETag") };
    } catch (error) {
      signal.throwIfAborted();
      if (error instanceof OSSError) throw error;
      throw new OSSError(0);
    }
  };
}
