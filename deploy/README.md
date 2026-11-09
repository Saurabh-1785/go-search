# Azure Deployment Guide — go-search

Deploys **go-search** to **Azure Container Apps** (consumption tier, scales to zero), with the search index persisted on an **Azure File Share**, so it survives restarts and redeploys.

---

## Architecture

```
Internet ──HTTPS──▶ Container Apps ingress (TLS, appends X-Forwarded-For)
                      │
                      ▼
                    container app "go-search"  (max 1 replica, 0.5 vCPU / 1 GiB)
                      │  env: DATA_DIR=/mnt/data, TRUST_PROXY_HEADERS=true,
                      │       ADMIN_API_KEY ← secret "admin-api-key"
                      │  probes: startup + liveness on /health
                      │
                      └── mount /mnt/data ──▶ Azure File Share "go-search-data"
                                               └── index/
                                                   ├── CURRENT          ← names the active generation
                                                   ├── gen-000003/      ← previous (fallback)
                                                   └── gen-000004/      ← active
                                                       ├── dictionary.json  docs.json
                                                       └── postings.bin     contents.bin
```

**Why one replica?** The index, rate limiter and job list live in process memory, and only one process may write the index on the share. Several replicas would serve different results and race on saves.

---

## Files

| File | Purpose |
|---|---|
| `azure.sh` | The only script. Settings sit at the top (each one can be overridden by exporting it). Commands: `up`, `update`, `down` |
| `README.md` | This guide |

```bash
./deploy/azure.sh help
```

| Command | What it does |
|---|---|
| `up` | Create **or update** the whole stack. Safe to re-run: reuses resources, keeps the admin key |
| `update` | Code change → new image → new revision. Leaves infrastructure, secret and data alone |
| `down` | Delete the app and environment, **keep** the index (storage) |
| `down --everything` | Delete the whole resource group, including the index |

---

## Prerequisites

| Tool | Check | Install |
|---|---|---|
| Azure CLI, logged in | `az account show` | `curl -sL https://aka.ms/InstallAzureCLIDeb \| sudo bash`, then `az login` |
| Docker, running | `docker info` | Docker Desktop. **On WSL:** Settings → Resources → WSL integration → enable your distro |
| Docker Hub login | `docker login` | Free account at [hub.docker.com](https://hub.docker.com) |
| `jq`, `curl`, `openssl`, `git` | `jq --version` | `sudo apt install jq curl openssl git` |

`up` and `update` check all of these first and stop with the fix if one is missing. Azure logins expire after 90 days without use; just run `az login` again.

> **Image visibility:** Docker Hub repositories are **public** by default. The image contains only the compiled binary (no secrets, no data), so that's acceptable. For a private image, use Azure Container Registry (Basic tier, ~$5/month).

---

## Deploy

```bash
cd /path/to/go-search
DOCKER_USER=yourDockerHubUsername ./deploy/azure.sh up
```

Different region or names:

```bash
LOCATION=westeurope STORAGE_ACCOUNT=gosearch$RANDOM DOCKER_USER=you ./deploy/azure.sh up
```

What it does (each step reuses what already exists):

| Step | Action |
|---|---|
| 0 | Preflight: tools, Azure login, Docker running, valid names |
| 1 | Register `Microsoft.App`, `Microsoft.OperationalInsights`, `Microsoft.Storage` (no-op after the first time) |
| 2 | Resource group `go-search-rg` in `centralindia` |
| 3 | Storage account (HTTPS only, TLS 1.2+, no public blob access) and a 5 GiB file share |
| 4 | Build `linux/amd64` image, push as `docker.io/<you>/gosearch:<UTC time>-<git sha>` |
| 5 | Container Apps environment (creates a Log Analytics workspace for logs) |
| 6 | Register the share with the environment (refreshes the storage key on re-runs) |
| 7 | Admin API key: `ADMIN_API_KEY` if you set it, else the **existing** key from the app's secret, else a new random one |
| 8 | PUT the full app spec, wait until provisioning succeeds and `/health` returns 200 |

At the end it prints the URL and, **only if the key is new**, the admin API key. Store it. Later you can read it with:

```bash
az containerapp secret show -n go-search -g go-search-rg --secret-name admin-api-key --query value -o tsv
```

> **Estimated time:** 5–10 minutes the first time, 2–3 minutes after.

---

## Verify

```bash
APP=https://$(az containerapp show -n go-search -g go-search-rg --query properties.configuration.ingress.fqdn -o tsv)
KEY=$(az containerapp secret show -n go-search -g go-search-rg --secret-name admin-api-key --query value -o tsv)

curl $APP/health

# Admin endpoints need the key — this must return 401:
curl -s -o /dev/null -w '%{http_code}\n' -X POST $APP/crawl -d '{}'

# Start a crawl
curl -X POST $APP/crawl -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://go.dev", "depth": 1, "max_pages": 20}'

# Follow it, then search
curl -H "Authorization: Bearer $KEY" $APP/crawl/jobs
curl "$APP/search?q=goroutines"
```

### Persistence check

```bash
curl $APP/index/stats                 # note doc_count
DOCKER_USER=you ./deploy/azure.sh update    # new revision = new container
curl $APP/index/stats                 # same doc_count: loaded from the share on startup
```

The index is saved after every crawl and when the container gets `SIGTERM` (scale to zero, new revision), and loaded on startup. With `minReplicas: 0`, Azure can scale the app to zero **during** a crawl, since a crawl is not an HTTP request. What was indexed so far is saved; the rest of the crawl stops. If long crawls must finish, set `minReplicas` to 1 in `cmd_up` of `azure.sh` (costs more: the replica runs all the time).

You can browse the files in the portal: **Storage accounts → gosearchstorage → File shares → go-search-data → index/**.

---

## Update after a code change

```bash
DOCKER_USER=yourDockerHubUsername ./deploy/azure.sh update
```

Builds, pushes a new unique tag, rolls out a new revision, waits for `/health`, and lists revisions. A new tag every time is what makes Azure roll out: re-pushing `:latest` and "updating" to the same image string creates no new revision. Tags ending in `-dirty` were built from uncommitted changes.

Roll back to an earlier image:

```bash
az containerapp update -n go-search -g go-search-rg --image docker.io/you/gosearch:<older tag>
```

---

## Teardown

```bash
./deploy/azure.sh down               # delete app + environment, KEEP the index (storage)
./deploy/azure.sh down --everything  # delete the whole resource group, including the index
```

Both ask for confirmation. After `down`, `up` recreates the app and it loads the existing index from the share.

---

## Cost estimate (Azure for Students)

| Resource | SKU | Est. cost |
|---|---|---|
| Storage account + 5 GiB share | Standard LRS | ~$0.10–0.30/month |
| Container Apps | Consumption, 0.5 vCPU / 1 GiB, scales to zero | Within the monthly free grant for light use |
| Log Analytics | Pay per GB ingested | Cents for this app's log volume |
| **Total** | | **~$0–$1/month** at hobby traffic |

Prices change; check the Azure pricing calculator before relying on these numbers.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `✖ Azure CLI is not logged in (or the login expired)` | `az login` |
| `✖ Docker is not running` on WSL | Docker Desktop → Settings → Resources → WSL integration → enable the distro |
| `✖ Storage account name … is taken globally` | `STORAGE_ACCOUNT=gosearch$RANDOM DOCKER_USER=you ./deploy/azure.sh up` |
| `docker push failed` | `docker login`; check `DOCKER_USER` matches your Docker Hub username |
| Provisioning `Failed` | `az containerapp revision list -n go-search -g go-search-rg -o table`, then `az containerapp logs show -n go-search -g go-search-rg --tail 100` |
| Health check never passes | Logs (above). `Failed to load index` means a corrupt generation: point `index/CURRENT` at the previous `gen-…` directory in the share |
| Empty index after a restart | Logs should say `Loaded index from /mnt/data/index`. `No saved index` means the share isn't mounted: `az containerapp env storage show -n go-search-env -g go-search-rg --storage-name gosearchfiles` |
| `permission denied` writing `/mnt/data` | The share is mounted as UID 10001; `APP_UID` at the top of `azure.sh` must match the Dockerfile user |
| Everyone gets `429` at once | `TRUST_PROXY_HEADERS` must be `true` behind the ingress, or all users share one rate-limit bucket |
