#!/bin/sh
set -eu

template=$1
output=$2

case "${VITE_OSS_DIRECT_UPLOAD:-false}" in
  false)
    cp "$template" "$output"
    exit
    ;;
  true) ;;
  *)
    printf '%s\n' 'VITE_OSS_DIRECT_UPLOAD must be true or false' >&2
    exit 1
    ;;
esac

upload_origin=${VITE_OSS_UPLOAD_ORIGIN:-}
playback_origin=${VITE_OSS_PLAYBACK_ORIGIN:-$upload_origin}
# Keep wildcards, ports, paths and Nginx syntax out of the generated policy.
dns_label='[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?'
for origin in "$upload_origin" "$playback_origin"; do
  if ! printf '%s\n' "$origin" | LC_ALL=C grep -Eq "^https://($dns_label\\.)+$dns_label$"; then
    printf '%s\n' 'VITE_OSS_UPLOAD_ORIGIN and VITE_OSS_PLAYBACK_ORIGIN must be exact HTTPS origins' >&2
    exit 1
  fi
  case "$origin" in
    *-internal.*)
      printf '%s\n' 'Browser media origins must use public endpoints' >&2
      exit 1
      ;;
  esac
done
# grep matches one line at a time. Check the entire values before using sed.
case "$upload_origin$playback_origin" in
  *[!a-z0-9:/.-]*)
    printf '%s\n' 'Unexpected character in media origin' >&2
    exit 1
    ;;
esac

sed "s|img-src 'self' https: http:|img-src 'self' $playback_origin|; \
s|media-src 'self' https: http:|media-src 'self' $playback_origin|; \
s|connect-src 'self';|connect-src 'self' $upload_origin;|" "$template" > "$output"
