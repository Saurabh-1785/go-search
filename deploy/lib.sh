# deploy/lib.sh — helpers shared by the Azure scripts. Sourced, not executed.

step() { printf '\n▶ %s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
die()  { printf '\n✖ %s\n' "$*" >&2; exit 1; }

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# require_tools fails early, with a fix, if a tool or login is missing.
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
  [[ -n "${DOCKER_USER:-}" ]] || die "Set DOCKER_USER to your Docker Hub username, e.g. DOCKER_USER=me $0"
}

# image_tag returns a unique, traceable tag: <UTC timestamp>-<git sha>[-dirty].
# A new tag on every deploy is what makes Azure create a new revision —
# re-pushing ':latest' and "updating" to the same string does nothing.
image_tag() {
  local sha
  sha="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo nogit)"
  if [[ -n "$(git -C "$PROJECT_ROOT" status --porcelain 2>/dev/null)" ]]; then
    sha="${sha}-dirty"
  fi
  printf '%s-%s' "$(date -u +%Y%m%d%H%M%S)" "$sha"
}

# build_and_push builds the image for linux/amd64 and pushes it. Prints the
# full image reference on stdout (everything else goes to stderr).
build_and_push() {
  local image tag
  tag="$(image_tag)"
  image="docker.io/${DOCKER_USER}/${IMAGE_REPO}:${tag}"
  step "Building image ${image}" >&2
  docker build --platform linux/amd64 -t "$image" "$PROJECT_ROOT" >&2
  step "Pushing image (requires: docker login)" >&2
  docker push "$image" >&2 || die "docker push failed. Run 'docker login' and check the repository name."
  printf '%s' "$image"
}

app_exists() {
  az containerapp show -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" --output none 2>/dev/null
}

# wait_provisioned polls until the container app's provisioning finishes.
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

app_fqdn() {
  az containerapp show -n "$CONTAINER_APP_NAME" -g "$RESOURCE_GROUP" \
    --query properties.configuration.ingress.fqdn --output tsv
}

# wait_healthy polls /health until it returns 200. With minReplicas 0 the
# first request triggers a cold start and also loads the index from disk.
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
