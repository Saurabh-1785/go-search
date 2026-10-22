#!/usr/bin/env bash
# deploy/update.sh — Rebuild & redeploy after a code change
# Usage: DOCKER_USER=you ./deploy/update.sh

set -euo pipefail

DOCKER_USER="${DOCKER_USER:?Please set DOCKER_USER}"
IMAGE="${DOCKER_USER}/gosearch:latest"
RESOURCE_GROUP="go-search-rg"
CONTAINER_APP_NAME="go-search"

echo "▶ Building & pushing ${IMAGE}..."
docker build --platform linux/amd64 -t "${IMAGE}" ..
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
