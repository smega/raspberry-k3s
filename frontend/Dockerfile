FROM node:24-alpine AS build
WORKDIR /app

COPY package.json vite.config.js index.html ./
COPY src ./src
COPY public ./public
RUN npm install

# "Last deploy" line on the page; set by CI (.github/workflows/build-image.yml).
ARG VITE_BUILD_SHA=""
ARG VITE_BUILD_TIME=""
ARG VITE_BUILD_MESSAGE=""
ENV VITE_BUILD_SHA=$VITE_BUILD_SHA VITE_BUILD_TIME=$VITE_BUILD_TIME VITE_BUILD_MESSAGE=$VITE_BUILD_MESSAGE
RUN npm run build

FROM nginx:1.30-alpine
# Pick up Alpine fixes that landed after the nginx image was built
# (Trivy, 2026-11-15: libxslt).
RUN apk upgrade --no-cache
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]