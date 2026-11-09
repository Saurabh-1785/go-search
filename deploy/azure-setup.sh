#!/usr/bin/env bash
# =============================================================================
# deploy/azure-setup.sh — create or update the whole go-search stack on
# Azure Container Apps. Safe to re-run: existing resources are reused, and the
# existing admin API key is kept.
#
# Prerequisites: az (logged in: az login), docker (running, docker login),
#                jq, curl, openssl, git
#
# Usage:
#   DOCKER_USER=yourDockerHubUsername ./deploy/azure-setup.sh
#
# Optional overrides (see deploy/azure.env): LOCATION, RESOURCE_GROUP,
# STORAGE_ACCOUNT, ... and ADMIN_API_KEY to set a specific key.
#
# What it creates:
#   resource group → storage account (+ file share for the index)
#   → Container Apps environment (+ the share registered as storage)
#   → container app: 1 replica max, HTTPS ingress, health probes,
#     admin key as a secret, share mounted at /mnt/data
# =============================================================================
set -euo pipefail

DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=deploy/azure.env
source "${DEPLOY_DIR}/azure.env"
# shellcheck source=deploy/lib.sh
source "${DEPLOY_DIR}/lib.sh"

# ── 0. Preflight ──────────────────────────────────────────────────────────────
step "[0/8] Preflight checks"
require_tools az docker jq curl openssl git
require_docker_user
require_az_login
require_docker
[[ "$STORAGE_ACCOUNT" =~ ^[a-z0-9]{3,24}$ ]] \
  || die "STORAGE_ACCOUNT must be 3-24 lowercase letters/digits (got '${STORAGE_ACCOUNT}')."
[[ -z "${ADMIN_API_KEY:-}" || ${#ADMIN_API_KEY} -ge 16 ]] \
  || die "ADMIN_API_KEY must be at least 16 characters."

# ── 1. Resource providers (one-time per subscription; no-op afterwards) ─────
step "[1/8] Registering resource providers"
for ns in Microsoft.App Microsoft.OperationalInsights Microsoft.Storage; do
  az provider register --namespace "$ns" --wait --output none
  info "$ns registered"
done

# ── 2. Resource group ────────────────────────────────────────────────────────
step "[2/8] Resource group ${RESOURCE_GROUP} (${LOCATION})"
az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none

# ── 3. Storage account + file share (holds the index across restarts) ───────
step "[3/8] Storage account ${STORAGE_ACCOUNT} and share ${SHARE_NAME}"
if az storage account show -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" --output none 2>/dev/null; then
  info "Storage account exists — reusing it (existing index data is kept)"
  az storage account update -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" \
    --https-only true --min-tls-version TLS1_2 --allow-blob-public-access false \
    --output none
else
  available="$(az storage account check-name --name "$STORAGE_ACCOUNT" --query nameAvailable --output tsv)"
  [[ "$available" == "true" ]] || die "Storage account name '${STORAGE_ACCOUNT}' is taken globally.
  Pick another: STORAGE_ACCOUNT=gosearch$RANDOM DOCKER_USER=${DOCKER_USER} $0"
  # Hardened defaults: HTTPS only, TLS 1.2+, no anonymous blob access.
  az storage account create -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" -l "$LOCATION" \
    --sku Standard_LRS --kind StorageV2 \
    --https-only true --min-tls-version TLS1_2 --allow-blob-public-access false \
    --output none
  info "Created storage account"
fi

# share-rm goes through Azure Resource Manager, so no storage key is needed here.
if [[ "$(az storage share-rm exists -g "$RESOURCE_GROUP" --storage-account "$STORAGE_ACCOUNT" \
        -n "$SHARE_NAME" --query exists --output tsv)" == "true" ]]; then
  info "File share exists — reusing it"
else
  az storage share-rm create -g "$RESOURCE_GROUP" --storage-account "$STORAGE_ACCOUNT" \
    -n "$SHARE_NAME" --quota "$SHARE_QUOTA_GB" --output none
  info "Created file share (${SHARE_QUOTA_GB} GiB)"
fi

# ── 4. Build and push the image (unique tag per deploy) ──────────────────────
step "[4/8] Container image"
IMAGE="$(build_and_push)"
info "Image: ${IMAGE}"

# ── 5. Container Apps environment ───────────────────────────────────────────
step "[5/8] Container Apps environment ${CONTAINER_APP_ENV}"
if az containerapp env show -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --output none 2>/dev/null; then
  info "Environment exists — reusing it"
else
  # Creates a Log Analytics workspace for container logs (small cost per GB).
  az containerapp env create -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" -l "$LOCATION" --output none
  info "Created environment"
fi

# ── 6. Register the file share with the environment ─────────────────────────
step "[6/8] Attaching file share to the environment as '${ENV_STORAGE_NAME}'"
# 'storage set' is an upsert: re-running refreshes the account key. The key
# is read into a variable and passed to az only — never printed.
STORAGE_KEY="$(az storage account keys list -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" \
  --query '[0].value' --output tsv)"
az containerapp env storage set -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" \
  --storage-name "$ENV_STORAGE_NAME" \
  --azure-file-account-name "$STORAGE_ACCOUNT" \
  --azure-file-account-key "$STORAGE_KEY" \
  --azure-file-share-name "$SHARE_NAME" \
  --access-mode ReadWrite --output none
unset STORAGE_KEY

# ── 7. Admin API key: explicit > existing secret > new ──────────────────────
step "[7/8] Admin API key"
KEY_IS_NEW=false
if [[ -n "${ADMIN_API_KEY:-}" ]]; then
  info "Using ADMIN_API_KEY from the environment"
elif app_exists && ADMIN_API_KEY="$(az containerapp secret show -n "$CONTAINER_APP_NAME" \
        -g "$RESOURCE_GROUP" --secret-name admin-api-key --query value --output tsv 2>/dev/null)" \
     && [[ -n "$ADMIN_API_KEY" ]]; then
  info "Keeping the existing admin key from the app's secret"
else
  ADMIN_API_KEY="$(openssl rand -hex 32)"
  KEY_IS_NEW=true
  info "Generated a new admin key"
fi
(( ${#ADMIN_API_KEY} >= 16 )) || die "ADMIN_API_KEY must be at least 16 characters."

# ── 8. Create or update the container app (ARM PUT = full desired state) ────
# Why az rest and not 'az containerapp create': the create command cannot
# declare volumes, and its --yaml input has had parsing bugs. A PUT of the
# full JSON spec is idempotent: same input, same result.
#
# maxReplicas is 1 on purpose: the index, rate limiter and job list live in
# process memory, and only one process may write the index on the share.
step "[8/8] Deploying container app ${CONTAINER_APP_NAME}"
SUBSCRIPTION_ID="$(az account show --query id --output tsv)"
ENV_ID="$(az containerapp env show -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --query id --output tsv)"

# The body holds the admin key: mktemp creates it with mode 0600, and the
# trap removes it however the script exits. jq builds the JSON, so values are
# escaped properly whatever they contain.
BODY_FILE="$(mktemp "${TMPDIR:-/tmp}/go-search-app-XXXXXX.json")"
trap 'rm -f "$BODY_FILE"' EXIT

jq -n \
  --arg location "$LOCATION" \
  --arg envId "$ENV_ID" \
  --arg image "$IMAGE" \
  --arg key "$ADMIN_API_KEY" \
  --arg storageName "$ENV_STORAGE_NAME" \
  --arg mountOptions "uid=${APP_UID},gid=${APP_UID},dir_mode=0750,file_mode=0640" \
  '{
    location: $location,
    properties: {
      managedEnvironmentId: $envId,
      configuration: {
        activeRevisionsMode: "Single",
        ingress: { external: true, targetPort: 8080, transport: "auto", allowInsecure: false },
        secrets: [ { name: "admin-api-key", value: $key } ]
      },
      template: {
        scale: { minReplicas: 0, maxReplicas: 1 },
        volumes: [ {
          name: "index-data", storageType: "AzureFile", storageName: $storageName,
          mountOptions: $mountOptions
        } ],
        containers: [ {
          name: "go-search",
          image: $image,
          resources: { cpu: 0.5, memory: "1Gi" },
          env: [
            { name: "DATA_DIR",            value: "/mnt/data" },
            { name: "PORT",                value: "8080" },
            { name: "TRUST_PROXY_HEADERS", value: "true" },
            { name: "ADMIN_API_KEY",       secretRef: "admin-api-key" }
          ],
          volumeMounts: [ { volumeName: "index-data", mountPath: "/mnt/data" } ],
          probes: [
            { type: "Startup",  httpGet: { path: "/health", port: 8080 },
              initialDelaySeconds: 2, periodSeconds: 3, failureThreshold: 40 },
            { type: "Liveness", httpGet: { path: "/health", port: 8080 },
              periodSeconds: 30, failureThreshold: 3 }
          ]
        } ]
      }
    }
  }' > "$BODY_FILE"

az rest --method PUT \
  --url "https://management.azure.com/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.App/containerApps/${CONTAINER_APP_NAME}?api-version=2024-03-01" \
  --headers "Content-Type=application/json" \
  --body "@${BODY_FILE}" \
  --output none
rm -f "$BODY_FILE"

wait_provisioned
FQDN="$(app_fqdn)"
wait_healthy "$FQDN"

# ── Done ─────────────────────────────────────────────────────────────────────
cat <<EOF

════════════════════════════════════════════════════════════
  ✅  go-search is live
  URL      : https://${FQDN}
  Health   : https://${FQDN}/health
  Image    : ${IMAGE}
  Index    : ${STORAGE_ACCOUNT} → file share ${SHARE_NAME} → index/
EOF
if [[ "$KEY_IS_NEW" == true ]]; then
  cat <<EOF

  Admin API key (new — store it now; re-runs keep it):
    ${ADMIN_API_KEY}
EOF
else
  cat <<EOF

  Admin API key unchanged. To view it:
    az containerapp secret show -n ${CONTAINER_APP_NAME} -g ${RESOURCE_GROUP} --secret-name admin-api-key --query value -o tsv
EOF
fi
cat <<EOF

  Try:  curl -X POST https://${FQDN}/crawl \\
          -H "Authorization: Bearer \$ADMIN_API_KEY" \\
          -d '{"url":"https://go.dev","max_pages":20}'
════════════════════════════════════════════════════════════
EOF
