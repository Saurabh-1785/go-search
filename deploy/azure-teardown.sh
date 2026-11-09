#!/usr/bin/env bash
# =============================================================================
# deploy/azure-teardown.sh — stop paying for go-search on Azure.
#
#   ./deploy/azure-teardown.sh              delete the container app and its
#                                           environment; KEEP the storage
#                                           account (your index survives, and
#                                           azure-setup.sh picks it up again)
#   ./deploy/azure-teardown.sh --everything delete the whole resource group,
#                                           including the index data
#
# Both modes ask for confirmation. Deletion cannot be undone.
# =============================================================================
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/azure.env
source "${DEPLOY_DIR}/azure.env"
# shellcheck source=deploy/lib.sh
source "${DEPLOY_DIR}/lib.sh"

MODE="app"
case "${1:-}" in
  "") ;;
  --everything) MODE="everything" ;;
  *) die "Unknown option '$1'. Use no option, or --everything." ;;
esac

require_tools az
require_az_login

if [[ "$MODE" == "everything" ]]; then
  printf '\nThis PERMANENTLY deletes resource group %s, including the index in %s.\n' \
    "$RESOURCE_GROUP" "$STORAGE_ACCOUNT"
  read -r -p "Type the resource group name to confirm: " answer
  [[ "$answer" == "$RESOURCE_GROUP" ]] || die "Not confirmed. Nothing deleted."
  az group delete -n "$RESOURCE_GROUP" --yes --no-wait
  info "Deletion started (runs in the background on Azure)."
  exit 0
fi

printf '\nThis deletes container app %s and environment %s.\n' "$CONTAINER_APP_NAME" "$CONTAINER_APP_ENV"
printf 'Storage account %s (the index) is kept.\n' "$STORAGE_ACCOUNT"
read -r -p "Continue? [y/N] " answer
[[ "$answer" =~ ^[Yy]$ ]] || die "Not confirmed. Nothing deleted."

if app_exists; then
  az containerapp delete -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" --yes --output none
  info "Deleted container app"
else
  info "Container app not found — skipping"
fi
if az containerapp env show -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --output none 2>/dev/null; then
  az containerapp env delete -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --yes --output none
  info "Deleted environment"
else
  info "Environment not found — skipping"
fi
info "Done. Recreate any time with: DOCKER_USER=<you> ./deploy/azure-setup.sh"
