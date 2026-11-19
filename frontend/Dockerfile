#
# Multi-stage build for React (Vite) frontend, served by Nginx.
#
# Build:
#   docker build -f frontend/Dockerfile -t feedsystem-frontend .
#

FROM node:24-alpine AS build
WORKDIR /src
ARG VITE_OSS_DIRECT_UPLOAD=false
ARG VITE_SECURE_SESSIONS=false
ARG VITE_OSS_UPLOAD_ORIGIN
ARG VITE_OSS_PLAYBACK_ORIGIN

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
# Local environment and test credentials must not enter build layers.
RUN test -z "$(find . -maxdepth 1 -name '*.local' -print)" \
    && sh scripts/render-security-headers.sh security-headers.conf /tmp/security-headers.conf \
    && npm run build

FROM nginx:1.30.7-alpine
ARG VITE_SECURE_SESSIONS=false
ARG VITE_OSS_DIRECT_UPLOAD=false
ARG VITE_OSS_UPLOAD_ORIGIN
ARG VITE_OSS_PLAYBACK_ORIGIN
LABEL io.feedsystem.sessions.secure="${VITE_SECURE_SESSIONS}" \
      io.feedsystem.oss.direct="${VITE_OSS_DIRECT_UPLOAD}"       io.feedsystem.oss.upload-origin="${VITE_OSS_UPLOAD_ORIGIN}"       io.feedsystem.oss.playback-origin="${VITE_OSS_PLAYBACK_ORIGIN}"
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/nginx-http.conf /etc/nginx/conf.d/00-feedsystem.conf
COPY frontend/nginx-routes.conf frontend/api-proxy.conf /etc/nginx/
COPY --from=build /tmp/security-headers.conf /etc/nginx/security-headers.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]

