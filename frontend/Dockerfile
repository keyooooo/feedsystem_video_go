#
# Multi-stage build for Vue (Vite) frontend, served by Nginx.
#
# Build:
#   docker build -f frontend/Dockerfile -t feedsystem-frontend .
#

FROM node:24-alpine AS build
WORKDIR /src

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
ARG FRONTEND_BUILD_SCRIPT=build
RUN npm run "$FRONTEND_BUILD_SCRIPT" && if [ "$FRONTEND_BUILD_SCRIPT" = "build:react" ]; then cp -r dist-react dist; fi

FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]

