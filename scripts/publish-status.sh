#!/usr/bin/env bash
# Builds and pushes the status page images (status-web, status-api,
# status-checker) for arm64. status-web gets the current commit baked in for
# the "Last deploy" line.
#
# Usage: scripts/publish-status.sh [web|api|checker ...]   (default: all three)
# Requires: docker (logged in to registry.smdoes.eu).
#
# Rolling out is a separate step:
#   kubectl -n public-demo rollout restart deployment/status-web deployment/status-api
# status-checker needs no rollout: the CronJob pulls :latest on its next run.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REGISTRY="registry.smdoes.eu/raspberry"
TARGETS=("$@")
[ ${#TARGETS[@]} -eq 0 ] && TARGETS=(web api checker)

SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
if [ -n "$(git -C "$REPO_ROOT" status --porcelain -- .)" ]; then
  SHA="$SHA-dirty"
fi
MESSAGE="$(git -C "$REPO_ROOT" log -1 --format=%s)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

for t in "${TARGETS[@]}"; do
  case "$t" in
    web)
      echo "==> status-web ($SHA)"
      docker build --platform linux/arm64 -t "$REGISTRY/status-web:latest" \
        --build-arg VITE_BUILD_SHA="$SHA" \
        --build-arg VITE_BUILD_TIME="$BUILD_TIME" \
        --build-arg VITE_BUILD_MESSAGE="$MESSAGE" \
        "$REPO_ROOT/frontend"
      docker push "$REGISTRY/status-web:latest"
      ;;
    api)
      echo "==> status-api"
      docker build --platform linux/arm64 -t "$REGISTRY/status-api:latest" "$REPO_ROOT/backend"
      docker push "$REGISTRY/status-api:latest"
      ;;
    checker)
      echo "==> status-checker"
      docker build --platform linux/arm64 -t "$REGISTRY/status-checker:latest" "$REPO_ROOT/cron"
      docker push "$REGISTRY/status-checker:latest"
      ;;
    *)
      echo "unknown target: $t (web|api|checker)" >&2
      exit 1
      ;;
  esac
done
