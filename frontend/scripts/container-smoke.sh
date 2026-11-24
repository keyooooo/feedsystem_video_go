#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SMOKE_ID="shortvideo-smoke-${GITHUB_RUN_ID:-$$}"
SMOKE_SECRET=""
cleanup(){
  local result=$?
  if [ "$result" -ne 0 ]; then
    docker logs "$SMOKE_ID-frontend" 2>&1 || true
    docker logs "$SMOKE_ID-direct" 2>&1 || true
    docker logs "$SMOKE_ID-backend" 2>&1 || true
  fi
  if [ -n "$SMOKE_SECRET" ]; then rm -f -- "$SMOKE_SECRET"; fi; docker rm -f "$SMOKE_ID-frontend" "$SMOKE_ID-backend" "$SMOKE_ID-direct" >/dev/null 2>&1 || true; docker network rm "$SMOKE_ID" >/dev/null 2>&1 || true; }
trap cleanup EXIT
# Synthetic sentinel proves local files are excluded from the build context.
SMOKE_SECRET="$(mktemp "$ROOT/frontend/.container-smoke-XXXXXX.local")"
printf 'synthetic sentinel, not a credential\n' > "$SMOKE_SECRET"
docker build -f "$ROOT/frontend/Dockerfile" -t "$SMOKE_ID" "$ROOT"
docker network create "$SMOKE_ID" >/dev/null
docker run -d --name "$SMOKE_ID-backend" --network "$SMOKE_ID" --network-alias backend -v "$ROOT/frontend/scripts/smoke-backend.mjs:/server.mjs:ro" -v "$ROOT/frontend/e2e/fixtures:/fixtures:ro" node:24-alpine node /server.mjs >/dev/null
docker run -d --name "$SMOKE_ID-frontend" --network "$SMOKE_ID" -p 127.0.0.1::80 "$SMOKE_ID" >/dev/null
ADDRESS="http://$(docker port "$SMOKE_ID-frontend" 80)"
curl --retry 15 --retry-all-errors --retry-max-time 30 --retry-delay 1 --max-time 5 -fsS "$ADDRESS/api/healthz" | grep -q '"status":"ok"'
for route in / /video/1 /u/2 /messages/2 /moderation /reports/1; do
  curl -fsS "$ADDRESS$route" | grep -q 'id="app"'
done
# The upstream leaves the connection open so a buffering regression causes a timeout.
node --input-type=module - "$ADDRESS" <<'JS'
import assert from "node:assert/strict";
const response = await fetch(new URL("/api/moderation/stream?probe=sse", process.argv[2]), {
  headers: { Authorization: "Bearer container-smoke", Accept: "text/event-stream" },
  signal: AbortSignal.timeout(5000),
});
assert.equal(response.status, 200);
assert.match(response.headers.get("content-type") ?? "", /^text\/event-stream\b/);
assert.equal(response.headers.get("cache-control"), "no-store");
const reader = response.body.getReader();
try {
  const decoder = new TextDecoder();
  let event = "";
  while (!event.includes("\n\n")) {
    const { value, done } = await reader.read();
    assert.equal(done, false);
    event += decoder.decode(value, { stream: true });
  }
  assert.equal(event, "data: moderation-ready\n\n");
} finally {
  await reader.cancel();
}
JS
HEADERS="$(mktemp)"
BODY="$(mktemp)"
curl -fsS --range 0-63 -D "$HEADERS" "$ADDRESS/static/clip.mp4" -o "$BODY"
grep -q '206' "$HEADERS"
test "$(wc -c < "$BODY")" -eq 64
rm "$HEADERS" "$BODY"
# Exercise the built lazy routes, forms, upload and security policy in both engines.
cd "$ROOT/frontend"
E2E_BASE_URL="$ADDRESS" E2E_PRODUCTION=1 npx playwright test --workers=2
echo 'PASS: production container, deep routes, API, Range, cache, security headers and browsers'

# Exercise the generated direct-mode CSP separately using exact mock origins.
UPLOAD_ORIGIN="https://upload.example.invalid"
PLAYBACK_ORIGIN="https://media.example.invalid"
docker build -f "$ROOT/frontend/Dockerfile" -t "$SMOKE_ID-direct" \
  --build-arg VITE_OSS_DIRECT_UPLOAD=true \
  --build-arg "VITE_OSS_UPLOAD_ORIGIN=$UPLOAD_ORIGIN" \
  --build-arg "VITE_OSS_PLAYBACK_ORIGIN=$PLAYBACK_ORIGIN" "$ROOT"
docker run -d --name "$SMOKE_ID-direct" --network "$SMOKE_ID" -p 127.0.0.1::80 "$SMOKE_ID-direct" >/dev/null
DIRECT_ADDRESS="http://$(docker port "$SMOKE_ID-direct" 80)"
curl --retry 15 --retry-all-errors --retry-max-time 30 --retry-delay 1 --max-time 5 -fsS "$DIRECT_ADDRESS/api/healthz" | grep -q '"status":"ok"'
curl -fsS --range 0-63 "$DIRECT_ADDRESS/static/clip.mp4" -o /dev/null
E2E_BASE_URL="$DIRECT_ADDRESS" E2E_OSS_UPLOAD_ORIGIN="$UPLOAD_ORIGIN" E2E_OSS_PLAYBACK_ORIGIN="$PLAYBACK_ORIGIN" \
  npx playwright test --grep 'direct upload|OSS direct' --workers=2 --output test-results/production-direct
echo 'PASS: direct-upload production container, exact CSP origins and browser enforcement'
