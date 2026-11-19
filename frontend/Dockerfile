FROM node:24-alpine AS build
WORKDIR /app

COPY package.json package-lock.json vite.config.js index.html ./
COPY src ./src
COPY public ./public
RUN npm ci

# "Last deploy" line on the page; set by CI (.github/workflows/build-image.yml).
ARG VITE_BUILD_SHA=""
ARG VITE_BUILD_TIME=""
ARG VITE_BUILD_MESSAGE=""
ENV VITE_BUILD_SHA=$VITE_BUILD_SHA VITE_BUILD_TIME=$VITE_BUILD_TIME VITE_BUILD_MESSAGE=$VITE_BUILD_MESSAGE
RUN npm run build

# The unprivileged variant: nginx runs as uid 101 with its pid and temp
# files in /tmp, so the pod can be Pod Security 'restricted'. It still
# listens on 80; the pod allows that through a sysctl
# (deployments/04-status-web.yaml).
FROM nginxinc/nginx-unprivileged:1.30-alpine
USER root
# Pick up Alpine fixes that landed after the nginx image was built
# (Trivy, 2026-11-15: libxslt).
RUN apk upgrade --no-cache
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
USER 101
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]