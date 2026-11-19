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

# The official image, made to run as its nginx user (uid 101) the way
# nginxinc/nginx-unprivileged does it: pid and temp files in /tmp, no
# `user` directive. Not that image itself: it lags the official one
# (2026-11-19: 1.30.6 with a CRITICAL CVE, the official one was on 1.30.7
# a day after the release, and the Trivy gate in CI stopped the build).
# The pod can then be Pod Security 'restricted'; it still listens on 80,
# which the pod allows through the unprivileged-port sysctl.
FROM nginx:1.30-alpine
# Pick up Alpine fixes that landed after the nginx image was built
# (Trivy, 2026-11-15: libxslt).
RUN apk upgrade --no-cache
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
RUN sed -i -e '/^user /d' -e 's,^pid .*,pid /tmp/nginx.pid;,' /etc/nginx/nginx.conf \
 && sed -i '/^http {/a\    client_body_temp_path /tmp/client_temp;\n    proxy_temp_path /tmp/proxy_temp;\n    fastcgi_temp_path /tmp/fastcgi_temp;\n    uwsgi_temp_path /tmp/uwsgi_temp;\n    scgi_temp_path /tmp/scgi_temp;' /etc/nginx/nginx.conf
USER 101
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]