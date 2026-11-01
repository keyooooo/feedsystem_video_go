#
# Multi-stage build for React (Vite) frontend, served by Nginx.
#
# Build:
#   docker build -f frontend/Dockerfile -t feedsystem-frontend .
#

FROM node:24-alpine AS build
WORKDIR /src

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
# Local environment and test credentials must not enter build layers.
RUN test -z "$(find . -maxdepth 1 -name '*.local' -print)" && npm run build

FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]

