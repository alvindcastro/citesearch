# Docker + Azure Container Apps — Brainstorm & Implementation Plan

Everything needed to containerize citesearch and deploy it to Azure Container Apps.
Includes concrete file content, design decisions, tradeoffs, and operational patterns.

---

## Table of Contents

1. [What Needs to Change Before Containerizing](#what-needs-to-change-before-containerizing)
2. [Dockerfile Design Decisions](#dockerfile-design-decisions)
3. [The Dockerfile](#the-dockerfile)
4. [.dockerignore](#dockerignore)
5. [docker-compose for Local Development](#docker-compose-for-local-development)
6. [The `data/docs/` Problem — Documents Are Not Code](#the-datadocs-problem--documents-are-not-code)
7. [Azure Container Apps Concepts](#azure-container-apps-concepts)
8. [ACA Architecture for This Project](#aca-architecture-for-this-project)
9. [One Image or Two?](#one-image-or-two)
10. [Secrets and Config in ACA](#secrets-and-config-in-aca)
11. [Volume Mounts — Azure Files](#volume-mounts--azure-files)
12. [Scale Rules](#scale-rules)
13. [ACA Jobs for Ingestion](#aca-jobs-for-ingestion)
14. [Ingress: External vs. Internal](#ingress-external-vs-internal)
15. [Managed Identity on the Container App](#managed-identity-on-the-container-app)
16. [Blue/Green Deployments via Revisions](#bluegreen-deployments-via-revisions)
17. [GitHub Actions CI/CD Pipeline](#github-actions-cicd-pipeline)
18. [Azure Container Registry](#azure-container-registry)
19. [Full Deployment: az CLI Walkthrough](#full-deployment-az-cli-walkthrough)
20. [Graceful Shutdown — What Needs to Be Added to Go](#graceful-shutdown--what-needs-to-be-added-to-go)
21. [Local Dev Workflow After This](#local-dev-workflow-after-this)
22. [Cost Estimate](#cost-estimate)
23. [Implementation Order](#implementation-order)

---

## What Needs to Change Before Containerizing

Three things in the Go code must be addressed before a container works correctly.

### 1. Swagger docs must generate at build time

`docs/` is gitignored and only exists after running `go generate ./internal/api/`.
The Dockerfile must run this step during the build — the container cannot serve Swagger otherwise.

**Solution:** Run `go install github.com/swaggo/swag/cmd/swag@latest && swag init` in the
builder stage before `go build`.

### 2. The `.env` file must not be the config source

`config.Load()` calls `godotenv.Load()` first, then falls back to real env vars. In a container,
there is no `.env` file — config comes from environment variables set in the container runtime.
This already works correctly: `godotenv.Load()` logs "No .env file found" and continues.
**No code change needed.** Do not `COPY .env` into the image.

### 3. Graceful shutdown is missing

When ACA stops a container (scale-down, redeploy), it sends `SIGTERM`. The current `router.Run()`
call blocks forever and ignores signals. In-flight requests will be hard-killed.

**Needs to be added** — see [Graceful Shutdown](#graceful-shutdown--what-needs-to-be-added-to-go).

---

## Dockerfile Design Decisions

| Decision | Choice | Why |
|----------|--------|-----|
| Build strategy | Multi-stage | Builder image (~1 GB) stays out of the final image |
| Builder base | `golang:1.24-alpine` | Matches `go 1.24.0` in go.mod; Alpine keeps it small |
| Final base | `alpine:3.21` | Minimal (~8 MB). CGO is not used, so glibc not needed |
| CGO | Disabled (`CGO_ENABLED=0`) | All dependencies are pure Go — no C bindings |
| Target arch | `linux/amd64` | ACA runs on AMD64; set explicitly for cross-platform builds on Apple Silicon |
| One binary or two | Both binaries compiled, CMD selects one | One image, `CMD` switches between HTTP and gRPC |
| Swagger generation | At build time in builder stage | `docs/` is gitignored; must exist in the final image |
| Run as root | No — non-root user `appuser` | Security hardening; ACA allows non-root |
| `data/docs/` | Volume mount, not baked in | Documents change; they don't belong in the image |
| HEALTHCHECK | Yes — `GET /health` | ACA uses it for readiness; Docker uses it for `docker ps` |

---

## The Dockerfile

```dockerfile
# ── Stage 1: Builder ─────────────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Install swag for Swagger doc generation
RUN go install github.com/swaggo/swag/cmd/swag@latest

# Download dependencies first (layer cache — only re-runs if go.mod/go.sum change)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Generate Swagger docs (writes to docs/ which is gitignored)
RUN swag init -g cmd/main.go --output docs

# Build both binaries
# CGO_ENABLED=0 → pure Go, no libc dependency in final image
# -ldflags "-s -w" → strip debug symbols (~30% smaller binary)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /dist/citesearch-http  ./cmd/main.go

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /dist/citesearch-grpc  ./cmd/grpc/main.go


# ── Stage 2: Final image ──────────────────────────────────────────────────────
FROM alpine:3.21

# CA certificates — required for HTTPS calls to Azure (OpenAI, Search, Blob)
RUN apk add --no-cache ca-certificates tzdata

# Non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /dist/citesearch-http  ./citesearch-http
COPY --from=builder /dist/citesearch-grpc  ./citesearch-grpc

# Copy generated Swagger docs
COPY --from=builder /build/docs ./docs

# Documents go here at runtime via volume mount — NOT baked into the image
RUN mkdir -p data/docs/banner data/docs/sop

# Own everything as appuser
RUN chown -R appuser:appgroup /app

USER appuser

# HTTP API port (matches API_PORT default)
EXPOSE 8000
# gRPC port (matches GRPC_PORT default)
EXPOSE 9000

# Healthcheck — ACA and Docker use this
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8000/health || exit 1

# Default: run the HTTP server
# Override with CMD ["./citesearch-grpc"] for the gRPC container
CMD ["./citesearch-http"]
```

**Build and run locally:**

```bash
# Build
docker build -t citesearch:latest .

# Run HTTP server (reads env from .env via --env-file)
docker run --rm -p 8000:8000 \
  --env-file .env \
  -v "$(pwd)/data/docs:/app/data/docs:ro" \
  citesearch:latest

# Run gRPC server (override CMD)
docker run --rm -p 9000:9000 \
  --env-file .env \
  citesearch:latest \
  ./citesearch-grpc
```

**Image size:** Builder ~1.1 GB, final image ~25–35 MB.

---

## .dockerignore

Prevents large directories from being sent to the build context (speeds up `docker build`).

```dockerignore
# Git
.git
.gitignore

# Local development
.env
.env.*

# Generated (rebuilt in Dockerfile)
docs/
gen/

# Document data (mounted as volume at runtime)
data/

# Blog (not part of the application)
blog/

# IDE
.idea/
.vscode/
*.code-workspace

# OS
.DS_Store
Thumbs.db

# Test artifacts
*.test
coverage.out

# Go build cache
vendor/
```

**Why `data/` is excluded:** Documents can be gigabytes. They're mounted at runtime, not baked in.
**Why `docs/` is excluded:** Regenerated during the Docker build — including stale local docs would
be confusing and would be overwritten anyway.

---

## docker-compose for Local Development

For local development: HTTP server + gRPC server + shared document volume.

```yaml
# docker-compose.yml
services:

  citesearch-http:
    build:
      context: .
      dockerfile: Dockerfile
    image: citesearch:local
    container_name: citesearch-http
    ports:
      - "8000:8000"
    env_file:
      - .env                          # local secrets — never commit this
    volumes:
      - ./data/docs:/app/data/docs:ro # read-only: container reads docs, doesn't write them
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8000/health"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    restart: unless-stopped

  citesearch-grpc:
    image: citesearch:local       # same image, different CMD
    container_name: citesearch-grpc
    command: ["./citesearch-grpc"]
    ports:
      - "9000:9000"
    env_file:
      - .env
    depends_on:
      citesearch-http:
        condition: service_healthy     # wait until HTTP server is healthy
    restart: unless-stopped
```

**Usage:**

```bash
# Build image and start both servers
docker compose up --build

# Rebuild only (image changed)
docker compose build

# Start existing image
docker compose up -d

# Tail logs
docker compose logs -f citesearch-http

# Stop and remove containers
docker compose down

# Stop, remove containers AND volumes
docker compose down -v
```

**Why `depends_on: service_healthy`:** Both servers share config and use the same Azure services.
If the HTTP server fails (bad env vars), the gRPC server will too. Starting them independently
makes log noise harder to read.

### docker-compose.override.yml (local dev extras)

```yaml
# docker-compose.override.yml  — git-ignored, local overrides only
services:

  citesearch-http:
    environment:
      - GIN_MODE=debug               # verbose Gin output
      - LOG_LEVEL=debug
    volumes:
      - ./data/docs:/app/data/docs   # rw for local testing of ingest
```

`docker-compose.override.yml` is automatically merged with `docker-compose.yml` when present.
Keeps dev-only settings out of the main compose file.

---

## The `data/docs/` Problem — Documents Are Not Code

The ingestion endpoints (`POST /banner/ingest`, `POST /sop/ingest`) read from `data/docs/`
on the filesystem. In a container, there are three ways to handle this:

### Option A: Azure Files volume mount (recommended for ACA)

Mount an Azure Files share as `/app/data/docs` inside the container.
Upload documents to the share via Azure Storage Explorer, az CLI, or Azure Function blob-to-file sync.

```
Azure Files share: citesearch-docs
├── banner/
│   └── general/2026/february/Banner_General_9.3.37.2_ReleaseNotes.pdf
└── sop/
    └── SOP154 - Procedure - Start, Stop Axiom.docx
        ↓
Mounted at /app/data/docs in the Container App
```

**Pros:** Persistent across container restarts, survives deployments, easily managed.
**Cons:** Azure Files has per-transaction costs; latency is higher than local disk.

### Option B: Init container pre-download (sidestep volume mounts)

Run a short-lived init container that downloads documents from Blob Storage to a shared
ephemeral volume before the main container starts.

```yaml
# In ACA, init containers run before app containers
initContainers:
  - name: doc-downloader
    image: mcr.microsoft.com/azure-cli
    command: ["az", "storage", "blob", "download-batch",
              "--destination", "/app/data/docs",
              "--source", "banner-release-notes",
              "--connection-string", "$(AZURE_STORAGE_CONNECTION_STRING)"]
    volumeMounts:
      - name: docs-volume
        mountPath: /app/data/docs
```

**Pros:** No ongoing Azure Files cost; documents are always fresh on startup.
**Cons:** Startup time increases proportionally to document count. Fails loudly if Blob is empty.

### Option C: Don't use the filesystem at all (future refactor)

The cleanest long-term solution: instead of reading files from disk during ingest, stream them
directly from Azure Blob Storage. `/banner/blob/sync` already does this — make it the only ingest path.

```
Before: POST /banner/ingest reads from /app/data/docs (filesystem)
After:  POST /banner/ingest streams from Azure Blob (no filesystem dependency)
```

This eliminates the volume mount concern entirely. The container becomes truly stateless.
**Not a quick change** — would require refactoring the ingestion pipeline.

---

## Azure Container Apps Concepts

| Concept | Meaning |
|---------|---------|
| **Environment** | Shared networking boundary. All Container Apps in an environment share a VNet and can reach each other by name. |
| **Container App** | A managed container with ingress, scaling, and revision management. |
| **Revision** | An immutable snapshot of a Container App at a point in time. Traffic can be split between revisions (blue/green). |
| **Replica** | A running instance of a revision. ACA manages scaling replicas up/down. |
| **Ingress** | HTTP/gRPC traffic rules. External (internet-facing) or internal (environment-only). |
| **Scale rule** | Triggers that control replica count. HTTP, CPU, memory, KEDA-based (Service Bus queue depth, etc.). |
| **Job** | A Container App that runs to completion (one-shot or scheduled). Perfect for ingestion. |
| **Secret** | A key-value secret stored in ACA, injected as env vars or volume mounts. |
| **Dapr** | Optional sidecar for service-to-service calls, pub/sub, state management. |

---

## ACA Architecture for This Project

```
┌─────────────────────────────────────────────────────────────────┐
│  Azure Container Apps Environment: citesearch-env                 │
│                                                                 │
│  ┌──────────────────────────┐   ┌──────────────────────────┐   │
│  │  Container App:          │   │  Container App:          │   │
│  │  citesearch-http           │   │  citesearch-grpc           │   │
│  │                          │   │                          │   │
│  │  Image: citesearch     │   │  Image: citesearch     │   │
│  │  CMD: ./citesearch-http    │   │  CMD: ./citesearch-grpc    │   │
│  │  Port: 8000              │   │  Port: 9000              │   │
│  │  Ingress: External HTTPS │   │  Ingress: Internal only  │   │
│  │  Scale: 0–3 replicas     │   │  Scale: 0–2 replicas     │   │
│  │  Min: 0 (scale-to-zero)  │   │                          │   │
│  │                          │   │                          │   │
│  │  Volume: Azure Files     │   │                          │   │
│  │  /app/data/docs          │   │                          │   │
│  └──────────────────────────┘   └──────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────┐                                   │
│  │  ACA Job:                │                                   │
│  │  citesearch-ingest-job     │                                   │
│  │                          │                                   │
│  │  Trigger: manual / cron  │                                   │
│  │  Runs to completion      │                                   │
│  │  Calls /banner/ingest    │                                   │
│  └──────────────────────────┘                                   │
└─────────────────────────────────────────────────────────────────┘
         │                    │
         │ HTTPS              │ Internal gRPC
         ▼                    ▼
  Internet clients     LangGraph agents
  n8n workflows        Azure Functions
  Bruno / curl
```

---

## One Image or Two?

**Decision: One image, two CMD values.**

Both binaries are compiled into the same image. The Container App's `command` field selects which
one runs.

**Why not two images:**
- Would require maintaining two build pipelines
- Config, dependencies, and code are identical — no reason to diverge
- A single image tag (`citesearch:1.2.3`) covers both servers

**In ACA:**
```yaml
# HTTP server container app
template:
  containers:
    - image: citesearchacr.azurecr.io/citesearch:1.2.3
      command: ["./citesearch-http"]

# gRPC server container app (separate Container App, same image)
template:
  containers:
    - image: citesearchacr.azurecr.io/citesearch:1.2.3
      command: ["./citesearch-grpc"]
```

---

## Secrets and Config in ACA

**Never pass secrets as plain env vars in ACA.** Use the secrets mechanism.

### Step 1: Store secrets in ACA

```bash
az containerapp secret set \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --secrets \
    openai-api-key="<key>" \
    search-api-key="<key>" \
    storage-connection="<conn-string>"
```

### Step 2: Reference secrets as env vars

```bash
az containerapp env var set \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --env-vars \
    "AZURE_OPENAI_API_KEY=secretref:openai-api-key" \
    "AZURE_SEARCH_API_KEY=secretref:search-api-key" \
    "AZURE_STORAGE_CONNECTION_STRING=secretref:storage-connection" \
    "AZURE_OPENAI_ENDPOINT=https://citesearch-openai.openai.azure.com/" \
    "AZURE_SEARCH_ENDPOINT=https://citesearch-search.search.windows.net" \
    "AZURE_OPENAI_API_VERSION=2024-02-01" \
    "API_PORT=8000" \
    "CHUNK_SIZE=500" \
    "CHUNK_OVERLAP=25" \
    "TOP_K_DEFAULT=5"
```

### Better: Azure Key Vault references (recommended)

Store secrets in Key Vault, reference them from ACA using Managed Identity. No secret values
are ever stored in ACA itself.

```bash
# Grant Container App's managed identity access to Key Vault
az keyvault set-policy --name citesearch-kv \
  --object-id <container-app-principal-id> \
  --secret-permissions get

# Reference Key Vault secret in ACA
az containerapp secret set \
  --name citesearch-http \
  --secrets "openai-api-key=keyvaultref:https://citesearch-kv.vault.azure.net/secrets/openai-api-key,identityref:/subscriptions/.../userAssignedIdentities/citesearch-identity"
```

---

## Volume Mounts — Azure Files

Mount an Azure Files share into the container for the `data/docs/` directory.

### Create the share

```bash
# Create storage account (if not using the existing blob one)
az storage account create \
  --name citesearchfiles \
  --resource-group citesearch-rg \
  --sku Standard_LRS

# Create the file share
az storage share create \
  --account-name citesearchfiles \
  --name citesearch-docs \
  --quota 10  # GB
```

### Add the storage to the ACA Environment

```bash
az containerapp env storage set \
  --name citesearch-env \
  --resource-group citesearch-rg \
  --storage-name citesearch-docs-storage \
  --azure-file-account-name citesearchfiles \
  --azure-file-account-key "<storage-account-key>" \
  --azure-file-share-name citesearch-docs \
  --access-mode ReadOnly
```

### Mount in the Container App

```yaml
# In the Container App YAML
template:
  volumes:
    - name: docs-vol
      storageType: AzureFile
      storageName: citesearch-docs-storage
  containers:
    - name: citesearch-http
      volumeMounts:
        - mountPath: /app/data/docs
          volumeName: docs-vol
```

### Upload documents to the share

```bash
# Using az CLI
az storage file upload-batch \
  --account-name citesearchfiles \
  --destination citesearch-docs/banner/general/2026/february \
  --source ./data/docs/banner/general/2026/february

# Or use Azure Storage Explorer (GUI)
# Or use the Azure portal
```

---

## Scale Rules

### HTTP-based scaling (default, simplest)

```bash
az containerapp update \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --min-replicas 0 \
  --max-replicas 3 \
  --scale-rule-name http-rule \
  --scale-rule-type http \
  --scale-rule-http-concurrency 10
```

`min-replicas 0` → **scale to zero** when idle. First request after idle has a cold start
(~5–10 seconds for this image). Acceptable for an internal tool; not for latency-sensitive production.

Set `min-replicas 1` to eliminate cold starts at the cost of always paying for one replica.

### KEDA-based: Service Bus queue depth

If async ingest jobs are queued in Service Bus, scale the ingest job worker based on queue depth.

```bash
az containerapp update \
  --name citesearch-ingest-worker \
  --scale-rule-name sb-rule \
  --scale-rule-type azure-servicebus \
  --scale-rule-metadata \
    "queueName=ingest-jobs" \
    "messageCount=5" \
    "namespace=citesearch-servicebus"
```

One replica spins up for every 5 messages in the queue. Scales to zero when queue is empty.

---

## ACA Jobs for Ingestion

**Key insight:** Ingestion is not a long-running service — it's a batch job that runs to completion.
ACA Jobs are the right primitive, not a Container App replica.

### Manual Job: On-Demand Ingest

```bash
# Create the job (one time)
az containerapp job create \
  --name citesearch-ingest-job \
  --resource-group citesearch-rg \
  --environment citesearch-env \
  --image citesearchacr.azurecr.io/citesearch:latest \
  --command "/bin/sh" "-c" \
    "wget -qO- -X POST http://citesearch-http/banner/ingest \
     -H 'Content-Type: application/json' \
     -d '{\"overwrite\":false}'" \
  --replica-timeout 3600 \
  --replica-retry-limit 1

# Run it on demand
az containerapp job start \
  --name citesearch-ingest-job \
  --resource-group citesearch-rg
```

### Scheduled Job: Daily Blob Sync

```bash
az containerapp job create \
  --name citesearch-daily-sync \
  --resource-group citesearch-rg \
  --environment citesearch-env \
  --trigger-type Schedule \
  --cron-expression "0 2 * * *" \   # 2:00 AM UTC daily
  --image citesearchacr.azurecr.io/citesearch:latest \
  --command "/bin/sh" "-c" \
    "wget -qO- -X POST http://citesearch-http/banner/blob/sync \
     -H 'Content-Type: application/json' \
     -d '{\"overwrite\":false}'"
```

**Why Jobs instead of an always-on replica calling itself:**
- Jobs are billed only for execution time
- ACA retries failed jobs automatically
- Job execution history is visible in Azure portal
- Cleaner separation: the API server serves requests; jobs do batch work

---

## Ingress: External vs. Internal

| Setting | URL Format | Use When |
|---------|-----------|---------|
| `external` | `https://citesearch-http.kindplant-abc.eastus.azurecontainerapps.io` | Browser clients, n8n cloud, external Azure Functions |
| `internal` | `http://citesearch-http` (within the environment) | Azure Functions in the same env, gRPC between containers |

```bash
# HTTP API: external (HTTPS only — ACA manages TLS automatically)
az containerapp ingress enable \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --type external \
  --target-port 8000 \
  --transport http

# gRPC server: internal only (no public exposure)
az containerapp ingress enable \
  --name citesearch-grpc \
  --resource-group citesearch-rg \
  --type internal \
  --target-port 9000 \
  --transport http2   # required for gRPC
```

ACA provisions a free TLS certificate for external ingress. No cert management needed.

### Custom Domain

```bash
az containerapp hostname add \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --hostname citesearch.yourdomain.com

# ACA will provide a TXT record and CNAME to add to your DNS
```

---

## Managed Identity on the Container App

Eliminates `AZURE_OPENAI_API_KEY`, `AZURE_SEARCH_API_KEY`, and `AZURE_STORAGE_CONNECTION_STRING`
from env vars. The container authenticates to Azure services by identity, not credentials.

### Enable identity

```bash
az containerapp identity assign \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --system-assigned
```

### Grant roles

```bash
# Azure OpenAI: Cognitive Services User
az role assignment create \
  --assignee <container-app-principal-id> \
  --role "Cognitive Services User" \
  --scope /subscriptions/.../resourceGroups/.../providers/Microsoft.CognitiveServices/accounts/citesearch-openai

# Azure AI Search: Search Index Data Contributor
az role assignment create \
  --assignee <container-app-principal-id> \
  --role "Search Index Data Contributor" \
  --scope /subscriptions/.../resourceGroups/.../providers/Microsoft.Search/searchServices/citesearch-search

# Blob Storage: Storage Blob Data Reader
az role assignment create \
  --assignee <container-app-principal-id> \
  --role "Storage Blob Data Reader" \
  --scope /subscriptions/.../resourceGroups/.../providers/Microsoft.Storage/storageAccounts/citesearchragstorage
```

### Code change required

`config/config.go` currently uses API key strings. To use Managed Identity, switch from
raw HTTP calls with `api-key` headers to `azidentity.DefaultAzureCredential` token acquisition.

For Azure AI Search and Azure OpenAI, this means:
1. Replace `api-key: <key>` header with `Authorization: Bearer <token>`
2. Token retrieved via `credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: ["https://cognitiveservices.azure.com/.default"]})`
3. `DefaultAzureCredential` uses Managed Identity in Azure, CLI/env credentials locally

This is a meaningful but worthwhile refactor. Until then, Key Vault references are the next-best option.

---

## Blue/Green Deployments via Revisions

ACA creates a new **revision** every time you update a Container App. Traffic can be split
between revisions for safe rollouts.

### Deploy a new version

```bash
# Deploy new image — creates revision citesearch-http--abc123
az containerapp update \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --image citesearchacr.azurecr.io/citesearch:1.3.0

# Current traffic: 100% on previous revision
# New revision is running but receiving 0% traffic
```

### Route 10% of traffic to new revision (canary)

```bash
az containerapp ingress traffic set \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --revision-weight \
    "citesearch-http--<old-revision>=90" \
    "citesearch-http--<new-revision>=10"
```

### Promote to 100% after validation

```bash
az containerapp ingress traffic set \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --revision-weight "citesearch-http--<new-revision>=100"
```

### Rollback if needed

```bash
# Instant rollback to previous revision
az containerapp ingress traffic set \
  --name citesearch-http \
  --resource-group citesearch-rg \
  --revision-weight "citesearch-http--<old-revision>=100"
```

No redeployment needed for rollback — the old revision is still running.

---

## GitHub Actions CI/CD Pipeline

On every push to `main`: build → push to ACR → deploy to ACA.

```yaml
# .github/workflows/deploy.yml
name: Build and Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:  # manual trigger

env:
  REGISTRY:     citesearchacr.azurecr.io
  IMAGE_NAME:   citesearch
  RESOURCE_GROUP: citesearch-rg
  ACA_HTTP_NAME:  citesearch-http
  ACA_GRPC_NAME:  citesearch-grpc

jobs:
  build-and-deploy:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Azure login
        uses: azure/login@v2
        with:
          client-id:       ${{ secrets.AZURE_CLIENT_ID }}
          tenant-id:       ${{ secrets.AZURE_TENANT_ID }}
          subscription-id: ${{ secrets.AZURE_SUBSCRIPTION_ID }}

      - name: Log in to ACR
        run: az acr login --name citesearchacr

      - name: Build and push image
        run: |
          IMAGE_TAG=${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:${{ github.sha }}
          IMAGE_LATEST=${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:latest

          docker build -t $IMAGE_TAG -t $IMAGE_LATEST .
          docker push $IMAGE_TAG
          docker push $IMAGE_LATEST

      - name: Deploy HTTP server to ACA
        run: |
          az containerapp update \
            --name ${{ env.ACA_HTTP_NAME }} \
            --resource-group ${{ env.RESOURCE_GROUP }} \
            --image ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:${{ github.sha }}

      - name: Deploy gRPC server to ACA
        run: |
          az containerapp update \
            --name ${{ env.ACA_GRPC_NAME }} \
            --resource-group ${{ env.RESOURCE_GROUP }} \
            --image ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:${{ github.sha }}

      - name: Verify deployment
        run: |
          # Wait for new revision to be healthy
          az containerapp revision list \
            --name ${{ env.ACA_HTTP_NAME }} \
            --resource-group ${{ env.RESOURCE_GROUP }} \
            --query "[0].properties.healthState"
```

**GitHub secrets to configure:**
- `AZURE_CLIENT_ID` — Service Principal or Federated Identity for OIDC login
- `AZURE_TENANT_ID`
- `AZURE_SUBSCRIPTION_ID`

Use `azure/login` with OIDC (no stored secrets) via:
```bash
az ad app federated-credential create \
  --id <app-id> \
  --parameters '{"name":"github-main","issuer":"https://token.actions.githubusercontent.com","subject":"repo:yourorg/citesearch:ref:refs/heads/main","audiences":["api://AzureADTokenExchange"]}'
```

### Add on-push tests

```yaml
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - run: go vet ./...
      - run: go test ./internal/ingest/... -v

  build-and-deploy:
    needs: test     # only deploys if tests pass
    ...
```

---

## Azure Container Registry

ACR stores the Docker image. ACA pulls from it directly.

```bash
# Create ACR (Basic tier — enough for this project)
az acr create \
  --name citesearchacr \
  --resource-group citesearch-rg \
  --sku Basic \
  --admin-enabled true

# Grant ACA pull access (Managed Identity preferred over admin credentials)
az role assignment create \
  --assignee <container-app-environment-principal-id> \
  --role "AcrPull" \
  --scope /subscriptions/.../resourceGroups/.../providers/Microsoft.ContainerRegistry/registries/citesearchacr
```

**ACR pricing:** Basic tier — $0.167/day (~$5/month) + $0.003/GB storage. For one image with a
few revisions, well under $1/month in storage.

---

## Full Deployment: az CLI Walkthrough

Complete first-time setup from scratch.

```bash
# Variables
RG="citesearch-rg"
LOCATION="canadacentral"
ENV="citesearch-env"
ACR="citesearchacr"
IMAGE="$ACR.azurecr.io/citesearch"

# 1. Resource group
az group create --name $RG --location $LOCATION

# 2. Container Registry
az acr create --name $ACR --resource-group $RG --sku Basic

# 3. Build and push image
az acr login --name $ACR
docker build -t $IMAGE:latest .
docker push $IMAGE:latest

# 4. Container Apps Environment
az containerapp env create \
  --name $ENV \
  --resource-group $RG \
  --location $LOCATION

# 5. HTTP server Container App
az containerapp create \
  --name citesearch-http \
  --resource-group $RG \
  --environment $ENV \
  --image $IMAGE:latest \
  --command "./citesearch-http" \
  --target-port 8000 \
  --ingress external \
  --min-replicas 0 \
  --max-replicas 3 \
  --registry-server $ACR.azurecr.io \
  --secrets \
    "openai-key=<AZURE_OPENAI_API_KEY>" \
    "search-key=<AZURE_SEARCH_API_KEY>" \
  --env-vars \
    "AZURE_OPENAI_ENDPOINT=https://citesearch-openai.openai.azure.com/" \
    "AZURE_OPENAI_API_KEY=secretref:openai-key" \
    "AZURE_SEARCH_ENDPOINT=https://citesearch-search.search.windows.net" \
    "AZURE_SEARCH_API_KEY=secretref:search-key" \
    "AZURE_OPENAI_API_VERSION=2024-02-01" \
    "AZURE_SEARCH_INDEX_NAME=citesearch-knowledge" \
    "CHUNK_SIZE=500" \
    "CHUNK_OVERLAP=25" \
    "TOP_K_DEFAULT=5" \
    "API_PORT=8000"

# 6. gRPC server Container App (internal only)
az containerapp create \
  --name citesearch-grpc \
  --resource-group $RG \
  --environment $ENV \
  --image $IMAGE:latest \
  --command "./citesearch-grpc" \
  --target-port 9000 \
  --ingress internal \
  --transport http2 \
  --min-replicas 0 \
  --max-replicas 2 \
  --registry-server $ACR.azurecr.io \
  --env-vars \
    "AZURE_OPENAI_ENDPOINT=https://citesearch-openai.openai.azure.com/" \
    "AZURE_OPENAI_API_KEY=secretref:openai-key" \
    "AZURE_SEARCH_ENDPOINT=https://citesearch-search.search.windows.net" \
    "AZURE_SEARCH_API_KEY=secretref:search-key" \
    "GRPC_PORT=9000"

# 7. Get the HTTPS URL
az containerapp show \
  --name citesearch-http \
  --resource-group $RG \
  --query properties.configuration.ingress.fqdn \
  --output tsv
# → citesearch-http.kindplant-abc.canadacentral.azurecontainerapps.io
```

---

## Graceful Shutdown — What Needs to Be Added to Go

ACA sends `SIGTERM` before stopping a container. The current `router.Run()` ignores it.
Add graceful shutdown to `cmd/main.go`:

```go
// cmd/main.go — graceful shutdown sketch
package main

import (
    "context"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "citesearch/config"
    "citesearch/internal/api"
)

func main() {
    cfg := config.Load()
    router := api.NewRouter(cfg)

    srv := &http.Server{
        Addr:    ":" + cfg.APIPort,
        Handler: router,
    }

    // Start server in goroutine
    go func() {
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()
    log.Printf("Listening on :%s", cfg.APIPort)

    // Wait for SIGTERM or SIGINT (Ctrl-C)
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
    <-quit

    log.Println("Shutting down — draining in-flight requests (30s max)...")
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := srv.Shutdown(ctx); err != nil {
        log.Printf("Forced shutdown: %v", err)
    }
    log.Println("Server stopped.")
}
```

ACA waits 30 seconds after `SIGTERM` before sending `SIGKILL`. Matching the `Shutdown` timeout
to 30 seconds means in-flight requests (e.g., a GPT-4o-mini call taking 3 seconds) complete
cleanly before the container exits.

---

## Local Dev Workflow After This

```bash
# First time
cp .env.example .env
# fill in Azure credentials

# Daily dev — no Go install needed
docker compose up --build        # builds image + starts both servers
docker compose logs -f           # tail logs
docker compose down              # stop

# Run tests (still uses local Go toolchain — faster feedback)
go test ./internal/ingest/...

# Build image only (no start)
docker compose build

# Force rebuild (e.g., go.sum changed)
docker compose build --no-cache
```

---

## Cost Estimate

| Resource | Tier | Estimated Monthly Cost |
|---------|------|----------------------|
| ACA — citesearch-http | Flex Consumption, scale-to-zero | $0–$2 (idle) / $5–$15 (moderate use) |
| ACA — citesearch-grpc | Flex Consumption, scale-to-zero | $0–$2 |
| ACA Jobs | Per execution | ~$0.01 per ingest run |
| Azure Container Registry | Basic | ~$5 |
| Azure Files (docs volume) | Standard LRS, 5 GB | ~$1 |
| **Total (light use)** | | **~$10–25/month** |

Scale-to-zero means the container costs nothing when idle. The biggest cost lever is how often
the containers receive requests and how long they run.

This is on top of existing Azure OpenAI + AI Search costs (~$1–5/month at low volume).

---

## Implementation Order

**Start here — unblocks everything else:**

1. ✅ Add `.dockerignore` — created; excludes `.env`, `data/`, `docs/`, `gen/`, `blog/`, IDE files
2. ✅ Write `Dockerfile` — multi-stage build; compiles both binaries; non-root user; healthcheck on `/health`
3. Build and run locally: `docker build . && docker run --env-file .env -p 8000:8000 ...`
4. ✅ Add `docker-compose.yml` — HTTP + gRPC services, shared `.env`, `data/docs` volume mount
5. Verify: `docker compose up` → `curl localhost:8000/health`

**Then Azure:**

6. Create ACR, push image (`az acr create` + `docker push`)
7. Create ACA Environment + HTTP Container App
8. Configure secrets (`--secrets` flags)
9. Verify: `curl https://<fqdn>/health`

**Then CI/CD:**

10. Add `.github/workflows/deploy.yml`
11. Configure OIDC federation (no secrets stored in GitHub)
12. Push to `main`, verify automatic deploy

**Then hardening:**

13. ✅ Graceful shutdown — `SIGTERM`/`SIGINT` handling with 30s drain window in `cmd/main.go`
14. Add Azure Files volume mount for `data/docs/`
15. Add gRPC Container App
16. Set up ACA Jobs for scheduled ingestion
17. Migrate to Managed Identity (eliminate API key secrets)

---

## Deployment Checklist

Progress tracker across all containerization and deployment phases.

### Phase 0 — Pre-flight (code changes before containerizing)

- [x] **Swagger generation at build time** — handled inside `Dockerfile` via `swag init`; no code change needed
- [x] **`.env` not required in container** — `config.Load()` already falls back to real env vars; already works
- [x] **Graceful shutdown** — added `SIGTERM`/`SIGINT` handling to `cmd/main.go`; uses `http.Server.Shutdown` with 30s timeout matching ACA's kill delay

### Phase 1 — Local Docker

- [x] **`.dockerignore`** — created; excludes `.env`, `data/`, `docs/`, `gen/`, `blog/`, IDE files
- [x] **`Dockerfile`** — multi-stage build (`golang:1.24-alpine` → `alpine:3.21`); compiles both binaries; non-root user; healthcheck on `/health`
- [ ] **Build image locally** — `docker build -t citesearch:latest .`
- [ ] **Run and verify** — `docker run --rm -p 8000:8000 --env-file .env citesearch:latest` → `curl localhost:8000/health` returns `200 OK`
- [x] **`docker-compose.yml`** — HTTP + gRPC services, shared `.env`, `data/docs` volume mount
- [ ] **Verify compose** — `docker compose up --build` → both servers healthy

### Phase 2 — Azure Container Registry

- [ ] **Create ACR** — `az acr create --name citesearchacr --sku Basic`
- [ ] **Push image** — `az acr login --name citesearchacr && docker push citesearchacr.azurecr.io/citesearch:latest`
- [ ] **Grant ACA pull access** — assign `AcrPull` role to the ACA environment's managed identity

### Phase 3 — Azure Container Apps

- [ ] **Create resource group** — `az group create --name citesearch-rg`
- [ ] **Create ACA Environment** — `az containerapp env create --name citesearch-env`
- [ ] **Deploy HTTP Container App** — external ingress, port 8000, scale 0–3
- [ ] **Configure secrets** — `AZURE_OPENAI_API_KEY`, `AZURE_SEARCH_API_KEY` via `az containerapp secret set`
- [ ] **Set env vars** — all non-secret config (`AZURE_OPENAI_ENDPOINT`, `CHUNK_SIZE`, etc.)
- [ ] **Verify** — `curl https://<fqdn>/health` returns `200 OK`
- [ ] **Deploy gRPC Container App** — internal ingress, port 9000, `http2` transport, same image

### Phase 4 — Document Volume

- [ ] **Create Azure Files share** — `az storage share create --name citesearch-docs`
- [ ] **Register share with ACA Environment** — `az containerapp env storage set`
- [ ] **Mount share in Container App** — `/app/data/docs` volume mount in container spec
- [ ] **Upload documents to share** — Banner PDFs and SOP DOCX files via Storage Explorer or `az storage file upload-batch`
- [ ] **Verify ingest** — `POST /banner/ingest` finds and indexes documents from the mounted share

### Phase 5 — CI/CD

- [ ] **`.github/workflows/deploy.yml`** — build → push to ACR → `az containerapp update` on push to `main` (template in *GitHub Actions* section above)
- [ ] **OIDC federation** — federated credential on the service principal so GitHub Actions logs in without stored secrets
- [ ] **Add `go vet` + `go test` step** — runs before deploy; blocks on failure
- [ ] **Verify end-to-end** — push a commit to `main`, watch the action, confirm new revision is live

### Phase 6 — Hardening

- [ ] **ACA Job: scheduled ingest** — daily cron job calls `/banner/blob/sync` (replaces manual trigger)
- [ ] **ACA Job: index health check** — every 30 min, alerts if `/index/stats` returns unexpectedly low count
- [ ] **Managed Identity** — replace `AZURE_OPENAI_API_KEY` and `AZURE_SEARCH_API_KEY` with token-based auth via `azidentity.DefaultAzureCredential` (eliminates stored secrets entirely)
- [ ] **Custom domain + TLS** — `az containerapp hostname add` + DNS CNAME
- [ ] **Blue/green smoke test** — deploy new revision at 0% traffic, validate `/health`, then shift to 100%

### Summary

| Phase | Status |
|-------|--------|
| 0 — Pre-flight | 3/3 done ✓ |
| 1 — Local Docker | 3/6 done |
| 2 — Container Registry | 0/3 done |
| 3 — Container Apps | 0/7 done |
| 4 — Document Volume | 0/5 done |
| 5 — CI/CD | 0/4 done |
| 6 — Hardening | 0/5 done |
