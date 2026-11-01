#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SMOKE_ID="shortvideo-smoke-${GITHUB_RUN_ID:-$$}"
cleanup(){ docker rm -f "$SMOKE_ID-frontend" "$SMOKE_ID-backend" >/dev/null 2>&1 || true; docker network rm "$SMOKE_ID" >/dev/null 2>&1 || true; }
trap cleanup EXIT
docker build -f "$ROOT/frontend/Dockerfile" --build-arg FRONTEND_BUILD_SCRIPT=build:react -t "$SMOKE_ID" "$ROOT"
docker network create "$SMOKE_ID" >/dev/null
docker run -d --name "$SMOKE_ID-backend" --network "$SMOKE_ID" --network-alias backend -v "$ROOT/frontend/scripts/smoke-backend.mjs:/server.mjs:ro" -v "$ROOT/frontend/e2e/fixtures:/fixtures:ro" node:24-alpine node /server.mjs >/dev/null
docker run -d --name "$SMOKE_ID-frontend" --network "$SMOKE_ID" -p 127.0.0.1::80 "$SMOKE_ID" >/dev/null
ADDRESS="http://$(docker port "$SMOKE_ID-frontend" 80)"
curl --retry 15 --retry-connrefused --retry-delay 1 -fsS "$ADDRESS/api/healthz" | grep -q '"status":"ok"'
for route in / /video/1 /u/2 /messages/2; do curl -fsS "$ADDRESS$route" | grep -q 'id="app"'; done
HEADERS="$(mktemp)"
BODY="$(mktemp)"
curl -fsS --range 0-63 -D "$HEADERS" "$ADDRESS/static/clip.mp4" -o "$BODY"
grep -q '206' "$HEADERS"
test "$(wc -c < "$BODY")" -eq 64
rm "$HEADERS" "$BODY"
echo 'PASS: production container, deep routes, API proxy and media Range proxy'
