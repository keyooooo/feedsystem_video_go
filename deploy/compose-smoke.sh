#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
project="feedsystem-release-ci-${GITHUB_RUN_ID:-$$}"
tmp="$(mktemp -d)"
export TLS_DIR="$tmp/tls"
export FEEDSYSTEM_PUBLIC_HOST=demo.example.invalid
export OSS_UPLOAD_ENDPOINT=https://media.example.invalid
export OSS_PLAYBACK_ENDPOINT="$OSS_UPLOAD_ENDPOINT"
export OSS_REGION=cn-guangzhou
export OSS_BUCKET=smoke-bucket
export OSS_UPLOAD_ROLE_ARN=acs:ram::123456789:role/ci-upload
export OSS_ACCESS_KEY_ID=ci-synthetic-id
export OSS_ACCESS_KEY_SECRET=ci-synthetic-secret
export MYSQL_IMAGE=mysql:8.0
export REDIS_IMAGE=redis:7.4-alpine
export RABBITMQ_IMAGE=rabbitmq:4.4.1-alpine
for name in MYSQL_ROOT_PASSWORD MYSQL_PASSWORD REDIS_PASSWORD RABBITMQ_PASS JWT_SECRET; do
  export "$name=$(openssl rand -hex 32)"
done

deploy/build-images.sh true "$OSS_UPLOAD_ENDPOINT" "$OSS_PLAYBACK_ENDPOINT" | tee "$tmp/images.log"
for name in API_IMAGE WORKER_IMAGE FRONTEND_IMAGE; do
  export "$name=$(sed -n "s/^${name}=//p" "$tmp/images.log")"
done
mkdir -p "$TLS_DIR/live/$FEEDSYSTEM_PUBLIC_HOST"
cert_dir="$TLS_DIR/live/$FEEDSYSTEM_PUBLIC_HOST"
openssl req -x509 -nodes -newkey rsa:2048 -sha256 -days 1 \
  -subj "/CN=$FEEDSYSTEM_PUBLIC_HOST" -addext "subjectAltName=DNS:$FEEDSYSTEM_PUBLIC_HOST" \
  -keyout "$cert_dir/privkey.pem" -out "$cert_dir/fullchain.pem" 2>/dev/null
cat >"$tmp/ci-ports.yml" <<'YAML'
services:
  frontend:
    ports: !override
      - "127.0.0.1::80"
      - "127.0.0.1::443"
YAML

compose=(docker compose -p "$project" -f compose.production.yml)
public=("${compose[@]}" -f deploy/compose.public.yml -f "$tmp/ci-ports.yml")
cleanup() {
  status=$?
  if [ "$status" != 0 ]; then "${public[@]}" logs --tail 25 || true; fi
  "${public[@]}" down --volumes >/dev/null || true
  rm -f "$cert_dir/fullchain.pem" "$cert_dir/privkey.pem" "$tmp/ci-ports.yml" "$tmp/images.log" "$tmp/headers"
  rmdir "$cert_dir" "$TLS_DIR/live" "$TLS_DIR" "$tmp" || true
}
trap cleanup EXIT
"${compose[@]}" config --format json | python3 -c 'import json, sys
services = json.load(sys.stdin)["services"]
assert set(services) == {"backend", "worker", "frontend", "mysql", "redis", "rabbitmq"}
assert all(not service.get("ports") for name, service in services.items() if name != "frontend")
assert services["frontend"]["ports"][0]["host_ip"] == "127.0.0.1"'
"${public[@]}" config --quiet
"${public[@]}" up --wait --wait-timeout 240

http_port="$("${public[@]}" port frontend 80)"
tls_port="$("${public[@]}" port frontend 443)"
tls_port="${tls_port##*:}"
tls_url="https://$FEEDSYSTEM_PUBLIC_HOST:$tls_port"
curl_tls=(curl --noproxy '*' --cacert "$cert_dir/fullchain.pem" --resolve "$FEEDSYSTEM_PUBLIC_HOST:$tls_port:127.0.0.1" --max-time 10)
redirect="$(curl -sS --noproxy '*' -H "Host: $FEEDSYSTEM_PUBLIC_HOST" -o /dev/null -w '%{redirect_url}' "http://$http_port/video/1")"
test "$redirect" = "https://$FEEDSYSTEM_PUBLIC_HOST/video/1"
"${curl_tls[@]}" -fsS -D "$tmp/headers" "$tls_url/api/readyz" | grep -q '"status":"ok"'
grep -qi '^strict-transport-security: max-age=' "$tmp/headers"
grep -qi 'connect-src.*https://media.example.invalid' "$tmp/headers"
"${curl_tls[@]}" -fsS "$tls_url/video/1" | grep -q 'id="app"'
status="$("${curl_tls[@]}" -sS -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' -d '{}' "$tls_url/api/uploads")"
test "$status" = 401
"${curl_tls[@]}" -fsS -H 'Content-Type: application/json' \
  -d '{"username":"release-check","password":"ci-fixture-password"}' "$tls_url/api/account/register" >/dev/null

rev="$(git rev-parse HEAD)"
test "$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$API_IMAGE")" = "$rev"
test "$(docker image inspect --format '{{index .Config.Labels "io.feedsystem.oss.upload-origin"}}' "$FRONTEND_IMAGE")" = "$OSS_UPLOAD_ENDPOINT"
printf '%s\n' 'PASS: production Compose, dependency readiness, TLS, HSTS, CSP, private ports, normal registration and upload authentication'
