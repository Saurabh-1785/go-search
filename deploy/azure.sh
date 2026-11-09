#!/usr/bin/env bash
# =============================================================================
# deploy/azure.sh — deploy go-search to Azure Container Apps.
#
#   DOCKER_USER=me ./deploy/azure.sh up         create or update everything
#                                               (safe to re-run; keeps the key)
#   DOCKER_USER=me ./deploy/azure.sh update     code change → new revision
#                  ./deploy/azure.sh down       delete app + environment,
#                                               KEEP the index (storage)
#                  ./deploy/azure.sh down --everything
#                                               delete the whole resource group
#
# Prerequisites: az (az login), docker (running, docker login), jq, curl,
#                openssl, git. The script checks them before touching Azure.
#
# Architecture:
#   resource group → storage account + file share (holds the index)
#   → Container Apps environment (share registered as storage)
#   → container app: 1 replica max, HTTPS ingress, health probes,
#     admin key as a secret, share mounted at /mnt/data
# =============================================================================
set -euo pipefail

# ── Settings: override any of them by exporting it first ────────────────────
#   e.g. LOCATION=westeurope STORAGE_ACCOUNT=gosearch$RANDOM DOCKER_USER=me ./deploy/azure.sh up
: "${RESOURCE_GROUP:=go-search-rg}"
: "${LOCATION:=centralindia}"            # pick a region close to your users
: "${STORAGE_ACCOUNT:=gosearchstorage}"  # GLOBAL name: 3-24 lowercase letters/digits
: "${SHARE_NAME:=go-search-data}"
: "${SHARE_QUOTA_GB:=5}"
: "${CONTAINER_APP_ENV:=go-search-env}"
: "${CONTAINER_APP_NAME:=go-search}"
: "${ENV_STORAGE_NAME:=gosearchfiles}"   # name of the share inside the environment
: "${IMAGE_REPO:=gosearch}"              # docker.io/<DOCKER_USER>/<IMAGE_REPO>:<tag>
: "${APP_UID:=10001}"                    # must match the user in the Dockerfile

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="./deploy/azure.sh"

# ── Output helpers ───────────────────────────────────────────────────────────
step() { printf '\n▶ %s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
die()  { printf '\n✖ %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '3,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

# ── Preflight checks: fail early, with the fix ──────────────────────────────
require_tools() {
  local t
  for t in "$@"; do
    command -v "$t" >/dev/null 2>&1 || die "'$t' is not installed (needed by this script)."
  done
}

require_az_login() {
  az account show --output none 2>/dev/null \
    || die "Azure CLI is not logged in (or the login expired). Run: az login"
  info "Subscription: $(az account show --query '[name, id]' --output tsv | paste -sd ' ')"
}

require_docker() {
  docker info >/dev/null 2>&1 || die "Docker is not running or not reachable.
  On WSL: Docker Desktop → Settings → Resources → WSL integration → enable this distro."
}

require_docker_user() {
  [[ -n "${DOCKER_USER:-}" ]] || die "Set DOCKER_USER to your Docker Hub username, e.g. DOCKER_USER=me ${SCRIPT} ${COMMAND}"
}

# ── Image: unique tag per deploy ─────────────────────────────────────────────
# <UTC timestamp>-<git sha>[-dirty]. A new tag on every deploy is what makes
# Azure create a new revision: re-pushing ':latest' and "updating" to the same
# image string does nothing.
image_tag() {
  local sha
  sha="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo nogit)"
  if [[ -n "$(git -C "$PROJECT_ROOT" status --porcelain 2>/dev/null)" ]]; then
    sha="${sha}-dirty"
  fi
  printf '%s-%s' "$(date -u +%Y%m%d%H%M%S)" "$sha"
}

# build_and_push prints the full image reference on stdout; logs go to stderr.
build_and_push() {
  local image
  image="docker.io/${DOCKER_USER}/${IMAGE_REPO}:$(image_tag)"
  step "Building image ${image}" >&2
  docker build --platform linux/amd64 -t "$image" "$PROJECT_ROOT" >&2
  step "Pushing image (requires: docker login)" >&2
  docker push "$image" >&2 || die "docker push failed. Run 'docker login' and check the repository name."
  printf '%s' "$image"
}

# ── Container app helpers ────────────────────────────────────────────────────
app_exists() {
  az containerapp show -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" --output none 2>/dev/null
}

env_exists() {
  az containerapp env show -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --output none 2>/dev/null
}

app_fqdn() {
  az containerapp show -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" \
    --query properties.configuration.ingress.fqdn --output tsv
}

wait_provisioned() {
  local state i
  for i in $(seq 1 60); do
    state="$(az containerapp show -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" \
      --query properties.provisioningState --output tsv 2>/dev/null || true)"
    case "$state" in
      Succeeded) info "Provisioning: Succeeded"; return 0 ;;
      Failed|Canceled) die "Provisioning ${state}. Inspect with:
  az containerapp revision list -n ${CONTAINER_APP_NAME} -g ${RESOURCE_GROUP} -o table" ;;
    esac
    info "Provisioning: ${state:-pending}... (${i}/60)"
    sleep 5
  done
  die "Timed out waiting for provisioning."
}

# wait_healthy polls /health until it returns 200. With minReplicas 0 the
# first request triggers a cold start, which also loads the index from disk.
wait_healthy() {
  local url="https://$1/health" i
  for i in $(seq 1 36); do
    if curl -fsS --max-time 10 "$url" >/dev/null 2>&1; then
      info "Health check OK: ${url}"
      return 0
    fi
    info "Waiting for ${url} ... (${i}/36)"
    sleep 5
  done
  die "App did not become healthy. Logs:
  az containerapp logs show -n ${CONTAINER_APP_NAME} -g ${RESOURCE_GROUP} --tail 100"
}

# =============================================================================
# up — create or update the whole stack (idempotent)
# =============================================================================
cmd_up() {
  step "[0/8] Preflight checks"
  require_tools az docker jq curl openssl git
  require_docker_user
  require_az_login
  require_docker
  [[ "$STORAGE_ACCOUNT" =~ ^[a-z0-9]{3,24}$ ]] \
    || die "STORAGE_ACCOUNT must be 3-24 lowercase letters/digits (got '${STORAGE_ACCOUNT}')."
  [[ -z "${ADMIN_API_KEY:-}" || ${#ADMIN_API_KEY} -ge 16 ]] \
    || die "ADMIN_API_KEY must be at least 16 characters."

  # 1. Resource providers: one-time per subscription, no-op afterwards.
  step "[1/8] Registering resource providers"
  local ns
  for ns in Microsoft.App Microsoft.OperationalInsights Microsoft.Storage; do
    az provider register --namespace "$ns" --wait --output none
    info "$ns registered"
  done

  step "[2/8] Resource group ${RESOURCE_GROUP} (${LOCATION})"
  az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none

  # 3. Storage: holds the index across restarts and redeploys.
  step "[3/8] Storage account ${STORAGE_ACCOUNT} and share ${SHARE_NAME}"
  if az storage account show -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" --output none 2>/dev/null; then
    info "Storage account exists — reusing it (existing index data is kept)"
    az storage account update -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" \
      --https-only true --min-tls-version TLS1_2 --allow-blob-public-access false \
      --output none
  else
    local available
    available="$(az storage account check-name --name "$STORAGE_ACCOUNT" --query nameAvailable --output tsv)"
    [[ "$available" == "true" ]] || die "Storage account name '${STORAGE_ACCOUNT}' is taken globally.
  Pick another: STORAGE_ACCOUNT=gosearch\$RANDOM DOCKER_USER=${DOCKER_USER} ${SCRIPT} up"
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

  step "[4/8] Container image"
  local image
  image="$(build_and_push)"
  info "Image: ${image}"

  step "[5/8] Container Apps environment ${CONTAINER_APP_ENV}"
  if env_exists; then
    info "Environment exists — reusing it"
  else
    # Also creates a Log Analytics workspace for container logs (small cost per GB).
    az containerapp env create -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" -l "$LOCATION" --output none
    info "Created environment"
  fi

  # 6. 'storage set' is an upsert: re-running refreshes the account key. The
  #    key goes into a variable and straight to az, never to the output.
  step "[6/8] Attaching file share to the environment as '${ENV_STORAGE_NAME}'"
  local storage_key
  storage_key="$(az storage account keys list -n "$STORAGE_ACCOUNT" -g "$RESOURCE_GROUP" \
    --query '[0].value' --output tsv)"
  az containerapp env storage set -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" \
    --storage-name "$ENV_STORAGE_NAME" \
    --azure-file-account-name "$STORAGE_ACCOUNT" \
    --azure-file-account-key "$storage_key" \
    --azure-file-share-name "$SHARE_NAME" \
    --access-mode ReadWrite --output none
  unset storage_key

  # 7. Admin key priority: explicit ADMIN_API_KEY > existing secret > new.
  step "[7/8] Admin API key"
  local key_is_new=false
  if [[ -n "${ADMIN_API_KEY:-}" ]]; then
    info "Using ADMIN_API_KEY from the environment"
  elif app_exists && ADMIN_API_KEY="$(az containerapp secret show -n "$CONTAINER_APP_NAME" \
          -g "$RESOURCE_GROUP" --secret-name admin-api-key --query value --output tsv 2>/dev/null)" \
       && [[ -n "$ADMIN_API_KEY" ]]; then
    info "Keeping the existing admin key from the app's secret"
  else
    ADMIN_API_KEY="$(openssl rand -hex 32)"
    key_is_new=true
    info "Generated a new admin key"
  fi

  # 8. ARM PUT of the full desired state. Why not 'az containerapp create':
  #    it cannot declare volumes, and its --yaml input has had parsing bugs.
  #    A PUT is idempotent: same input, same result.
  #    maxReplicas 1 on purpose: the index, rate limiter and job list live in
  #    process memory, and only one process may write the index on the share.
  step "[8/8] Deploying container app ${CONTAINER_APP_NAME}"
  local subscription_id env_id body_file
  subscription_id="$(az account show --query id --output tsv)"
  env_id="$(az containerapp env show -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --query id --output tsv)"

  # The body holds the admin key: mktemp creates it with mode 0600 and the
  # trap removes it however the script exits. jq escapes every value.
  body_file="$(mktemp "${TMPDIR:-/tmp}/go-search-app-XXXXXX.json")"
  # shellcheck disable=SC2064  # expand now: body_file is local to this function
  trap "rm -f '$body_file'" EXIT

  jq -n \
    --arg location "$LOCATION" \
    --arg envId "$env_id" \
    --arg image "$image" \
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
    }' > "$body_file"

  az rest --method PUT \
    --url "https://management.azure.com/subscriptions/${subscription_id}/resourceGroups/${RESOURCE_GROUP}/providers/Microsoft.App/containerApps/${CONTAINER_APP_NAME}?api-version=2024-03-01" \
    --headers "Content-Type=application/json" \
    --body "@${body_file}" \
    --output none
  rm -f "$body_file"

  wait_provisioned
  local fqdn
  fqdn="$(app_fqdn)"
  wait_healthy "$fqdn"

  cat <<EOF

════════════════════════════════════════════════════════════
  ✅  go-search is live
  URL      : https://${fqdn}
  Health   : https://${fqdn}/health
  Image    : ${image}
  Index    : ${STORAGE_ACCOUNT} → file share ${SHARE_NAME} → index/
EOF
  if [[ "$key_is_new" == true ]]; then
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

  Try:  curl -X POST https://${fqdn}/crawl \\
          -H "Authorization: Bearer \$ADMIN_API_KEY" \\
          -d '{"url":"https://go.dev","max_pages":20}'
════════════════════════════════════════════════════════════
EOF
}

# =============================================================================
# update — new image, new revision; infrastructure, secret and data untouched
# =============================================================================
cmd_update() {
  step "Preflight checks"
  require_tools az docker curl git
  require_docker_user
  require_az_login
  require_docker
  app_exists || die "Container app '${CONTAINER_APP_NAME}' not found in '${RESOURCE_GROUP}'.
  Create it first: DOCKER_USER=${DOCKER_USER} ${SCRIPT} up"

  local image fqdn
  image="$(build_and_push)"

  step "Rolling out ${image}"
  az containerapp update -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" --image "$image" --output none

  wait_provisioned
  fqdn="$(app_fqdn)"
  wait_healthy "$fqdn"

  step "Revisions"
  az containerapp revision list -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" \
    --query "[].{revision:name, active:properties.active, created:properties.createdTime, image:properties.template.containers[0].image}" \
    --output table

  printf '\n✅ Deployed %s to https://%s\n' "$image" "$fqdn"
}

# =============================================================================
# down — delete the app (keep the index), or everything with --everything
# =============================================================================
cmd_down() {
  local mode="app" answer
  case "${1:-}" in
    "") ;;
    --everything) mode="everything" ;;
    *) die "Unknown option '$1'. Use: ${SCRIPT} down [--everything]" ;;
  esac

  require_tools az
  require_az_login

  if [[ "$mode" == "everything" ]]; then
    printf '\nThis PERMANENTLY deletes resource group %s, including the index in %s.\n' \
      "$RESOURCE_GROUP" "$STORAGE_ACCOUNT"
    read -r -p "Type the resource group name to confirm: " answer
    [[ "$answer" == "$RESOURCE_GROUP" ]] || die "Not confirmed. Nothing deleted."
    az group delete -n "$RESOURCE_GROUP" --yes --no-wait
    info "Deletion started (runs in the background on Azure)."
    return 0
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
  if env_exists; then
    az containerapp env delete -n "$CONTAINER_APP_ENV" -g "$RESOURCE_GROUP" --yes --output none
    info "Deleted environment"
  else
    info "Environment not found — skipping"
  fi
  info "Done. Recreate any time with: DOCKER_USER=<you> ${SCRIPT} up"
}

# ── Dispatch ─────────────────────────────────────────────────────────────────
COMMAND="${1:-}"
shift || true
case "$COMMAND" in
  up)              cmd_up ;;
  update)          cmd_update ;;
  down)            cmd_down "$@" ;;
  ""|-h|--help|help) usage ;;
  *) usage >&2; die "Unknown command '${COMMAND}'." ;;
esac
