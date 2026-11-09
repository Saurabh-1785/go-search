#!/usr/bin/env bash
# =============================================================================
# deploy/update.sh — rebuild the image and roll out a new revision after a
# code change. Infrastructure, secrets and the index data are left as they are.
#
# Usage:  DOCKER_USER=yourDockerHubUsername ./deploy/update.sh
#
# Each run pushes a unique tag (<UTC time>-<git sha>), so Azure always creates
# a new revision. Re-pushing ':latest' would not: the image string wouldn't
# change, so 'containerapp update' would have nothing to roll out.
# =============================================================================
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/azure.env
source "${DEPLOY_DIR}/azure.env"
# shellcheck source=deploy/lib.sh
source "${DEPLOY_DIR}/lib.sh"

step "Preflight checks"
require_tools az docker curl git
require_docker_user
require_az_login
require_docker
app_exists || die "Container app '${CONTAINER_APP_NAME}' not found in '${RESOURCE_GROUP}'.
  Create it first: DOCKER_USER=${DOCKER_USER} ./deploy/azure-setup.sh"

IMAGE="$(build_and_push)"

step "Rolling out ${IMAGE}"
az containerapp update -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" --image "$IMAGE" --output none

wait_provisioned
FQDN="$(app_fqdn)"
wait_healthy "$FQDN"

step "Revisions"
az containerapp revision list -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" \
  --query "[].{revision:name, active:properties.active, created:properties.createdTime, image:properties.template.containers[0].image}" \
  --output table

printf '\n✅ Deployed %s to https://%s\n' "$IMAGE" "$FQDN"
