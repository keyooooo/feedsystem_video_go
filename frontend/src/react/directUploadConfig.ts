export const directUploadEnabled =
  import.meta.env.VITE_OSS_DIRECT_UPLOAD === "true";
export const cancelUpload = Symbol("cancel upload");

export function mediaPath(value: string | undefined) {
  if (!value || !/^https?:\/\//i.test(value)) return value;
  try {
    const url = new URL(value);
    if (url.pathname.startsWith("/static/")) return url.pathname + url.search;
  } catch {
    // Keep the original value and let the media element handle an invalid URL.
  }
  return value;
}
