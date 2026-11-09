#!/usr/bin/env bash
# deploy/update.sh — Rebuild & redeploy after a code change
# Usage: DOCKER_USER=you ./deploy/update.sh

set -euo pipefail

DOCKER_USER="${DOCKER_USER:?Please set DOCKER_USER}"
IMAGE="${DOCKER_USER}/gosearch:latest"
RESOURCE_GROUP="go-search-rg"
CONTAINER_APP_NAME="go-search"

echo "▶ Building & pushing ${IMAGE}..."
# Build from the project root no matter where the script is run from.
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
docker build --platform linux/amd64 -t "${IMAGE}" "${PROJECT_ROOT}"
docker push "${IMAGE}"

echo "▶ Updating container revision..."
az containerapp update \
  --name "${CONTAINER_APP_NAME}" \
  --resource-group "${RESOURCE_GROUP}" \
  --image "${IMAGE}" \
  --output table

echo "✅ New revision deployed."
az containerapp revision list \
  --name "${CONTAINER_APP_NAME}" \
  --resource-group "${RESOURCE_GROUP}" \
  --output table
