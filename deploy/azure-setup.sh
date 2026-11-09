#!/usr/bin/env bash
# =============================================================================
# deploy/azure-setup.sh
#
# Provisions all Azure resources for go-search and deploys the container.
#
# Prerequisites:
#   - az CLI installed and logged in  (az login)
#   - Docker installed and running
#   - Docker Hub account  (or swap to ACR — see comments below)
#
# Usage:
#   chmod +x deploy/azure-setup.sh
#   DOCKER_USER=yourDockerHubUsername ./deploy/azure-setup.sh
# =============================================================================

set -euo pipefail

# ── CONFIG — edit these if you want different names ──────────────────────────
RESOURCE_GROUP="go-search-rg"
LOCATION="centralindia"            # change to a region close to you
STORAGE_ACCOUNT="gosearchstorage"        # stable name — safe to re-run
SHARE_NAME="go-search-data"
SHARE_QUOTA_GB=5                  # 5 GB is within Azure free-tier allowance

CONTAINER_APP_ENV="go-search-env"
CONTAINER_APP_NAME="go-search"
IMAGE_NAME="gosearch"
IMAGE_TAG="latest"

# Docker Hub (free) — set DOCKER_USER in your shell before running
DOCKER_USER="${DOCKER_USER:?Please set DOCKER_USER to your Docker Hub username}"
FULL_IMAGE="${DOCKER_USER}/${IMAGE_NAME}:${IMAGE_TAG}"

# Admin API key for /crawl, /eval, /crawl/jobs, /search/stats.
# Pass ADMIN_API_KEY to keep an existing key; otherwise a new one is generated.
# It is stored as a Container Apps secret, not as a plain env var.
ADMIN_API_KEY="${ADMIN_API_KEY:-$(openssl rand -hex 32)}"
if [ "${#ADMIN_API_KEY}" -lt 16 ]; then
  echo "ADMIN_API_KEY must be at least 16 characters" >&2
  exit 1
fi

# Build from the project root no matter where the script is run from.
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── STEP 1: Resource Group ────────────────────────────────────────────────────
echo ""
echo "▶ [1/8] Creating resource group: ${RESOURCE_GROUP} (${LOCATION})"
az group create \
  --name "${RESOURCE_GROUP}" \
  --location "${LOCATION}" \
  --output table

# ── STEP 2: Storage Account ───────────────────────────────────────────────────
echo ""
echo "▶ [2/8] Creating storage account: ${STORAGE_ACCOUNT}"
echo "       (Standard LRS — cheapest option, ~\$0.02/GB/month)"
az storage account create \
  --name "${STORAGE_ACCOUNT}" \
  --resource-group "${RESOURCE_GROUP}" \
  --location "${LOCATION}" \
  --sku Standard_LRS \
  --kind StorageV2 \
  --output table

# Grab the storage account key for later steps
STORAGE_KEY=$(az storage account keys list \
  --account-name "${STORAGE_ACCOUNT}" \
  --resource-group "${RESOURCE_GROUP}" \
  --query "[0].value" \
  --output tsv)

# ── STEP 3: File Share ────────────────────────────────────────────────────────
echo ""
echo "▶ [3/8] Creating Azure File Share: ${SHARE_NAME} (${SHARE_QUOTA_GB} GiB)"
az storage share create \
  --name "${SHARE_NAME}" \
  --account-name "${STORAGE_ACCOUNT}" \
  --account-key "${STORAGE_KEY}" \
  --quota "${SHARE_QUOTA_GB}" \
  --output table

# Pre-create the index sub-directory inside the share
echo "       Creating index/ sub-directory in file share..."
az storage directory create \
  --share-name "${SHARE_NAME}" \
  --name "index" \
  --account-name "${STORAGE_ACCOUNT}" \
  --account-key "${STORAGE_KEY}" \
  --output table

# ── STEP 4: Build & Push Docker image ────────────────────────────────────────
echo ""
echo "▶ [4/8] Building Docker image: ${FULL_IMAGE}"
docker build \
  --platform linux/amd64 \
  -t "${FULL_IMAGE}" \
  "${PROJECT_ROOT}"

echo ""
echo "▶ [5/8] Pushing image to Docker Hub..."
echo "       (make sure you are logged in: docker login)"
docker push "${FULL_IMAGE}"

# ── STEP 5: Container Apps Environment ───────────────────────────────────────
echo ""
echo "▶ [6/8] Creating Container Apps environment: ${CONTAINER_APP_ENV}"
az containerapp env create \
  --name "${CONTAINER_APP_ENV}" \
  --resource-group "${RESOURCE_GROUP}" \
  --location "${LOCATION}" \
  --output table

# ── STEP 6: Attach the File Share as a storage volume ────────────────────────
echo ""
echo "▶ [7/8] Attaching Azure File Share to Container Apps environment"

# 'env storage set' cannot update share/account name — only the key.
# Delete any existing entry first so the step is safely re-runnable.
echo "       Removing any existing 'gosearchfiles' storage entry (safe to ignore if not found)..."
az containerapp env storage remove \
  --name "${CONTAINER_APP_ENV}" \
  --resource-group "${RESOURCE_GROUP}" \
  --storage-name "gosearchfiles" \
  --yes \
  --output none 2>/dev/null || true

az containerapp env storage set \
  --name "${CONTAINER_APP_ENV}" \
  --resource-group "${RESOURCE_GROUP}" \
  --storage-name "gosearchfiles" \
  --azure-file-account-name "${STORAGE_ACCOUNT}" \
  --azure-file-account-key "${STORAGE_KEY}" \
  --azure-file-share-name "${SHARE_NAME}" \
  --access-mode ReadWrite \
  --output table

# ── STEP 7: Create the Container App via ARM REST API ────────────────────────
# 'az containerapp create' has no --volume flags, and its --yaml flag has a
# known parsing bug (400 "System.Boolean" error). We bypass it entirely and
# call the ARM REST API directly with az rest, which accepts JSON natively.
#
# maxReplicas is 1 on purpose: the index, rate limiter and job list live in
# process memory, and only one process may write the index on the file share.
# Several replicas would each serve a different index and race on saves.
echo ""
echo "▶ [8/8] Deploying Container App: ${CONTAINER_APP_NAME}"

SUBSCRIPTION_ID=$(az account show --query id --output tsv)

ENV_ID=$(az containerapp env show \
  --name "${CONTAINER_APP_ENV}" \
  --resource-group "${RESOURCE_GROUP}" \
  --query id \
  --output tsv)

echo "       Subscription : ${SUBSCRIPTION_ID}"
echo "       Environment  : ${ENV_ID}"

# Write the request body as JSON (native API format, no YAML parsing).
# The file holds the admin key: mktemp creates it mode 0600, and the trap
# deletes it even if a later step fails.
JSON_FILE="$(mktemp /tmp/go-search-app-XXXXXX.json)"
trap 'rm -f "${JSON_FILE}"' EXIT
cat > "${JSON_FILE}" << JSON
{
  "location": "${LOCATION}",
  "properties": {
    "managedEnvironmentId": "${ENV_ID}",
    "configuration": {
      "ingress": {
        "external": true,
        "targetPort": 8080,
        "transport": "auto",
        "allowInsecure": false
      },
      "secrets": [
        { "name": "admin-api-key", "value": "${ADMIN_API_KEY}" }
      ]
    },
    "template": {
      "scale": {
        "minReplicas": 0,
        "maxReplicas": 1
      },
      "volumes": [
        {
          "name": "gosearch-vol",
          "storageType": "AzureFile",
          "storageName": "gosearchfiles"
        }
      ],
      "containers": [
        {
          "name": "go-search",
          "image": "${FULL_IMAGE}",
          "resources": {
            "cpu": 0.5,
            "memory": "1Gi"
          },
          "env": [
            { "name": "DATA_DIR",            "value": "/mnt/data" },
            { "name": "PORT",                "value": "8080" },
            { "name": "TRUST_PROXY_HEADERS", "value": "true" },
            { "name": "ADMIN_API_KEY",       "secretRef": "admin-api-key" }
          ],
          "volumeMounts": [
            {
              "volumeName": "gosearch-vol",
              "mountPath": "/mnt/data"
            }
          ]
        }
      ]
    }
  }
}
JSON

echo "       Sending PUT request to ARM API..."
az rest \
  --method PUT \
  --url "https://management.azure.com/subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.App/containerApps/${CONTAINER_APP_NAME}?api-version=2024-03-01" \
  --headers "Content-Type=application/json" \
  --body @"${JSON_FILE}" \
  --output none

rm -f "${JSON_FILE}"

echo ""
echo "       Waiting for provisioning to complete..."
az containerapp show \
  --name "${CONTAINER_APP_NAME}" \
  --resource-group "${RESOURCE_GROUP}" \
  --query "{name:name, state:properties.provisioningState, fqdn:properties.configuration.ingress.fqdn}" \
  --output table

# ── Done ──────────────────────────────────────────────────────────────────────
APP_URL=$(az containerapp show \
  --name "${CONTAINER_APP_NAME}" \
  --resource-group "${RESOURCE_GROUP}" \
  --query "properties.configuration.ingress.fqdn" \
  --output tsv)

echo ""
echo "════════════════════════════════════════════════════════"
echo "  ✅  Deployment complete!"
echo ""
echo "  App URL : https://${APP_URL}"
echo "  Health  : https://${APP_URL}/health"
echo ""
echo "  Storage account : ${STORAGE_ACCOUNT}"
echo "  File share      : ${SHARE_NAME}"
echo "  Mount path      : /mnt/data  (DATA_DIR inside container)"
echo ""
echo "  Admin API key (store it now — it is not shown again):"
echo "    ${ADMIN_API_KEY}"
echo "  Use it as:  curl -H 'Authorization: Bearer <key>' https://${APP_URL}/crawl/jobs"
echo ""
echo "  Crawl data lives at:"
echo "    Azure Portal → Storage Accounts → ${STORAGE_ACCOUNT}"
echo "    → File shares → ${SHARE_NAME} → index/"
echo "════════════════════════════════════════════════════════"
