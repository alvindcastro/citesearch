# How-To Guide

Task-focused recipes for running, ingesting, querying, and validating citesearch.

Use this guide when you already know what task you need to perform. For end-to-end setup paths,
start with [RUNBOOK.md](RUNBOOK.md).

---

## Before You Start

Most recipes assume:

- You are running commands from the repository root.
- `.env` exists and contains Azure OpenAI and Azure AI Search values.
- `jq` is available for formatting JSON responses.
- The backend listens on `http://localhost:8000` unless a command says otherwise.

---

## Start the Backend Locally

```bash
cp .env.example .env
# Fill in Azure OpenAI and Azure AI Search values.

go mod download
go run cmd/main.go
```

Verify:

```bash
curl -s http://localhost:8000/health | jq .
```

Expected: `status` is `ok` and the response names the configured chat model, embedding model,
and search index.

---

## Create or Recreate the Search Index

```bash
curl -s -X POST http://localhost:8000/index/create | jq .
curl -s http://localhost:8000/index/stats | jq .
```

Use this after changing index schema or when starting from an empty Azure AI Search resource.
If the index already exists, confirm that your local code expects the same schema before reusing it.

---

## Ingest Local Banner PDFs

Place release-note PDFs under the canonical folder tree:

```text
data/docs/banner/<module>/releases/<year>/<file>.pdf
```

Run a dry preflight first:

```bash
go test ./internal/ingest/... -run TestDryRun -v
```

Then ingest:

```bash
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{"docs_path":"data/docs/banner","overwrite":false}' | jq .
```

Verify indexed chunks:

```bash
curl -s http://localhost:8000/index/stats | jq .
curl -s http://localhost:8000/debug/chunks | jq '.[0:3]'
```

---

## Upload a PDF Without Filesystem Access

Use this path for Fly.io, Docker, or cloud deployments where you cannot place files under
`data/docs/`.

```bash
curl -s -X POST http://localhost:8000/banner/upload \
  -F "file=@Banner_Finance_9.3.22.pdf" \
  -F "source_type=banner" \
  -F "module=Finance" \
  -F "version=9.3.22" \
  -F "year=2026" | jq .
```

The upload creates Blob Storage state but does not index content. Chunking is the indexing step:

```bash
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"<upload-id>"}' | jq .
```

Check status:

```bash
curl -s http://localhost:8000/banner/upload/<upload-id>/status | jq .
```

Nice to know: Phase U upload accepts PDFs only. SOP, DOCX, TXT, and Markdown upload are rejected.
See [../ingest/PDF_UPLOAD_FLOW.md](../ingest/PDF_UPLOAD_FLOW.md).

---

## Upload a PDF From an Allowlisted URL

Set `UPLOAD_URL_ALLOWLIST` in `.env` if the host is not already allowed:

```env
UPLOAD_URL_ALLOWLIST=customercare.ellucian.com,ellucian.com
```

Then call:

```bash
curl -s -X POST http://localhost:8000/banner/upload/from-url \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://customercare.ellucian.com/path/to/Banner_Finance_9.3.22.pdf",
    "source_type": "banner",
    "module": "Finance",
    "version": "9.3.22",
    "year": "2026"
  }' | jq .
```

Follow with `POST /banner/upload/chunk` when you are ready to index.

---

## Ask a Question Directly Against the Backend

```bash
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What changed in Banner Finance 9.3.22?","module_filter":"Finance","top_k":5}' | jq .
```

For user guides:

```bash
curl -s -X POST http://localhost:8000/banner/finance/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"How do I find invoice information?","top_k":5}' | jq .
```

For SOPs:

```bash
curl -s -X POST http://localhost:8000/sop/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"How do I start Axiom?","top_k":5}' | jq .
```

---

## Run the Chatbot Adapter Locally

Start the backend first, then run the adapter in a second shell:

```bash
RAG_BACKEND_URL=http://localhost:8000 PORT=8080 go run cmd/server/main.go
```

Verify:

```bash
curl -s http://localhost:8080/health | jq .

curl -s -X POST http://localhost:8080/chat/ask \
  -H "Content-Type: application/json" \
  -d '{"message":"What changed in Banner 9.3.37?","session_id":"local-1"}' | jq .
```

---

## Regenerate Swagger Docs

Run this after changing public handler annotations or response models:

```bash
go generate ./internal/api/
```

Swagger output under `docs/` is generated and gitignored. Start the backend and open:

```text
http://localhost:8000/docs/index.html
```

---

## Regenerate gRPC Code

Run this after changing `proto/` definitions:

```bash
buf generate
go test ./internal/grpcserver/... ./cmd/grpc/... -v
```

Run the gRPC server:

```bash
go run cmd/grpc/main.go
grpcurl -plaintext localhost:9000 list
```

---

## Use The Bruno Collection

Open `apis/Omnivore RAG API/` in Bruno and set:

```text
base_url=http://localhost:8000
```

Use the System folder for health/index checks, then Banner or SOP folders for ingest and ask
flows.
