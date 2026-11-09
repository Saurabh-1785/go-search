# Azure Deployment Guide — go-search

This guide deploys **go-search** to **Azure Container Apps** (free consumption tier) with
crawl/index data persisted on an **Azure File Share** that survives container restarts.

---

## Architecture

```
Azure Container Apps  ──mount──▶  Azure File Share (go-search-data)
  /mnt/data/index/                 └── index/
                                       ├── CURRENT          ← names the active generation
                                       ├── gen-000003/      ← previous (fallback)
                                       └── gen-000004/      ← active
                                           ├── dictionary.json
                                           ├── docs.json
                                           ├── postings.bin
                                           └── contents.bin
```

The container env var `DATA_DIR=/mnt/data` is picked up by the app to read/write index files.

---

## Prerequisites

Install and log in to the Azure CLI:

```bash
# Install on Ubuntu/Debian
curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash

# Log in (opens browser for Azure for Students auth)
az login

# Verify your subscription
az account show
az account list --output table
```

Install Docker (if not already installed):

```bash
# Ubuntu/Debian quick-install
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER   # log out and back in after this
```

Create a free [Docker Hub](https://hub.docker.com) account if you don't have one, then log in:

```bash
docker login
```

---

## Step 1 — Register required Azure providers

```bash
az provider register --namespace Microsoft.App
az provider register --namespace Microsoft.OperationalInsights
# These can take 1-2 minutes; check with:
az provider show --namespace Microsoft.App --query registrationState
```

---

## Step 2 — Run the setup script

```bash
cd /path/to/go-search

# Make the scripts executable
chmod +x deploy/azure-setup.sh deploy/update.sh

# Set your Docker Hub username and run
DOCKER_USER=yourDockerHubUsername ./deploy/azure-setup.sh

# Re-running? Pass your existing key to keep it, otherwise a new one is generated:
# ADMIN_API_KEY=<existing key> DOCKER_USER=... ./deploy/azure-setup.sh
```

The script will:
1. Create resource group `go-search-rg` in `westeurope`
2. Create a Standard LRS Storage Account
3. Create a 5 GiB File Share `go-search-data`
4. Build the Docker image and push to Docker Hub
5. Create a Container Apps environment
6. Attach the File Share as a named volume
7. Deploy the container with `DATA_DIR=/mnt/data`, `TRUST_PROXY_HEADERS=true`, a single replica, and the admin API key stored as a Container Apps secret

At the end it prints the **admin API key**. Save it: you need it for `/crawl`, `/eval`, `/crawl/jobs` and `/search/stats`.

> **Why one replica?** The index, rate limiter and job list live in process memory, and only one process may write the index on the file share.

> **Estimated time:** 5–10 minutes

---

## Step 3 — Verify the deployment

```bash
# Get the app URL
APP_URL=$(az containerapp show \
  --name go-search \
  --resource-group go-search-rg \
  --query "properties.configuration.ingress.fqdn" \
  --output tsv)

echo "App URL: https://$APP_URL"

# Health check
curl https://$APP_URL/health

# Trigger a test crawl (admin key printed by the setup script)
ADMIN_API_KEY=<key from setup output>
curl -X POST https://$APP_URL/crawl \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://example.com",
    "depth": 1,
    "max_pages": 5,
    "workers": 2
  }'

# Check index stats (should show docs after crawl completes)
curl https://$APP_URL/index/stats
```

---

## Step 4 — Verify persistence across restarts

```bash
# Force a new revision (simulates a restart)
az containerapp update \
  --name go-search \
  --resource-group go-search-rg \
  --image yourDockerHubUsername/gosearch:latest

# Index stats should still show the same doc_count — data survived!
curl https://$APP_URL/index/stats
```

The index is saved after every crawl and when the container receives `SIGTERM` (scale-to-zero, new revision), then loaded on startup. With `minReplicas: 0`, Azure can scale the app to zero while a crawl runs, since a crawl is not an HTTP request. Whatever was indexed up to that point is saved. Set `minReplicas` to 1 if long crawls must finish.

You can also browse the files in **Azure Portal**:
`Storage Accounts → <your-account> → File shares → go-search-data → index/`

---

## Updating the app after code changes

```bash
DOCKER_USER=yourDockerHubUsername ./deploy/update.sh
```

---

## Cost estimate (Azure for Students)

| Resource | SKU | Est. Cost |
|---|---|---|
| Storage Account | Standard LRS | ~$0.02/GB/mo (5 GB ≈ $0.10/mo) |
| Azure File Share | 5 GiB quota | Included above |
| Container Apps | Consumption (0.5 vCPU, 1 GiB) | Free allowance: 180k vCPU-s/mo |
| **Total** | | **~$0–$1/month** |

> Azure for Students gives you $100 credit + free services — this stack fits easily within the free tier.

---

## Teardown (when done)

```bash
# Delete everything — storage, app, environment, all at once
az group delete --name go-search-rg --yes --no-wait
```

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `az containerapp create` fails with "extension not found" | `az extension add --name containerapp` |
| Container starts but `DATA_DIR` is wrong | Check env vars: `az containerapp show --name go-search -g go-search-rg --query "properties.template.containers[0].env"` |
| File share not mounted | Check volume: `az containerapp env storage show --name go-search-env -g go-search-rg --storage-name gosearchfiles` |
| Image pull errors | Re-run `docker login` and `docker push` |
