#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
revision="$(git rev-parse HEAD)"
if [ -n "$(git status --porcelain)" ]; then
  printf '%s\n' 'Build release images from a clean checkout.' >&2
  exit 1
fi

direct=${1:?usage: build-images.sh true|false [upload-origin] [playback-origin]}
upload_origin=${2:-}
playback_origin=${3:-$upload_origin}
case "$direct" in
  true) test -n "$upload_origin"; mode=direct ;;
  false) mode=legacy ;;
  *) exit 2 ;;
esac

repo_label="org.opencontainers.image.revision=$revision"
mysql="feedsystem-mysql:$revision"
docker build --label "$repo_label" -t "$mysql" - < deploy/mysql.Dockerfile
for target in api worker; do
  docker build --label "$repo_label" --target "$target" -f backend/Dockerfile \
    --build-arg "GOPROXY=${RELEASE_GOPROXY:-https://proxy.golang.org,direct}" \
    --build-arg "GOSUMDB=${RELEASE_GOSUMDB:-sum.golang.org}" \
    -t "feedsystem-$target:$revision" .
done

if [ "$mode" = direct ]; then
  origin_fingerprint="$(printf '%s\n' "$upload_origin" "$playback_origin" | sha256sum)"
  mode="direct-${origin_fingerprint:0:12}"
fi
frontend="feedsystem-frontend:$revision-$mode"
docker build --label "$repo_label" -f frontend/Dockerfile \
  --build-arg "VITE_OSS_DIRECT_UPLOAD=$direct" \
  --build-arg "VITE_OSS_UPLOAD_ORIGIN=$upload_origin" \
  --build-arg "VITE_OSS_PLAYBACK_ORIGIN=$playback_origin" \
  -t "$frontend" .

printf 'MYSQL_IMAGE=%s\nAPI_IMAGE=feedsystem-api:%s\nWORKER_IMAGE=feedsystem-worker:%s\nFRONTEND_IMAGE=%s\n' "$mysql" "$revision" "$revision" "$frontend"
docker image inspect --format '{{index .RepoTags 0}} {{.Id}}' \
  "$mysql" "feedsystem-api:$revision" "feedsystem-worker:$revision" "$frontend"
