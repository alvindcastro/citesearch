# Developer Guide

Engineering guide for changing citesearch without breaking local workflows, upload state, or
RAG behavior.

---

## Repo Map

| Path | Responsibility |
|---|---|
| `cmd/main.go` | HTTP backend entry point. Uses `API_PORT` (`8000` by default). |
| `cmd/server/main.go` | Botpress adapter entry point. Uses `PORT` (`8080` by default). |
| `cmd/grpc/main.go` | gRPC server entry point. Uses `GRPC_PORT` (`9000` by default). |
| `api/` | Chatbot adapter HTTP handlers: `/chat/ask`, `/chat/intent`, `/chat/sentiment`. |
| `internal/api/` | Main backend Gin handlers and router wiring. |
| `internal/rag/` | Query orchestration: embed, search, prompt, answer, confidence. |
| `internal/ingest/` | File walking, text extraction, chunking, embeddings, Search upload. |
| `internal/upload/` | Phase U upload/chunk sidecar services and test seams. |
| `internal/azure/` | Azure OpenAI, Azure AI Search, and Azure Blob integrations. |
| `internal/adapter/` | Adapter client that calls the backend from the Botpress-facing app. |
| `internal/intent/` | Rule-based chatbot intent classifier. |
| `internal/sentiment/` | Rule-based frustration/sentiment analyzer. |
| `proto/` | gRPC service definitions. |
| `gen/` | Generated gRPC Go code. Gitignored. |

---

## Architectural Boundaries

- Keep Azure SDK and live network dependencies out of unit tests.
- Keep request/response JSON structs close to the package that owns the behavior.
- Keep `internal/upload` independent from Gin. HTTP translation belongs in `internal/api`.
- Keep Botpress adapter behavior in `api/` and `internal/adapter`; backend RAG behavior belongs
  in `internal/api`, `internal/rag`, and `internal/ingest`.
- Prefer dependency seams for external systems: Blob storage, page counting, ingest execution,
  HTTP downloads, clocks, IDs, and backend HTTP clients.

---

## Change Workflow

Use the narrowest loop that proves the change:

1. Start with the package that owns the behavior.
2. Add or update focused tests before touching adjacent layers.
3. Wire handlers/routes only after the owning package behavior is stable.
4. Update docs when an operator-visible workflow, response, env var, or error changes.
5. Run broader tests before handoff when the change crosses package boundaries.

---

## Adding a Backend Endpoint

1. Add or update tests in `internal/api`.
2. Add request/response structs in the owning package when behavior is domain-specific.
3. Add handler logic in `internal/api/handlers.go` or a closely scoped file.
4. Wire the route in `internal/api/router.go`.
5. Add Swagger annotations if it is a public HTTP endpoint.
6. Run:

```bash
go test ./internal/api/... -v
go generate ./internal/api/
```

7. Update README or wiki docs when endpoint shape, env vars, operator workflow, or error
   behavior changes.

---

## Adding Upload/Chunk Behavior

Upload behavior is intentionally split:

- Upload routes store a PDF in Azure Blob Storage and create a sidecar.
- Upload routes must not call `ingest.Run()`, Azure OpenAI, or Azure AI Search.
- `POST /banner/upload/chunk` is the upload-flow endpoint that indexes uploaded pages.

When changing upload behavior:

1. Start with `internal/upload` unit tests for pure sidecar or service behavior.
2. Add `internal/api/upload_*_test.go` handler coverage for HTTP status and JSON shape.
3. Add or update `internal/api/upload_workflow_test.go` if an operator workflow changes.
4. Keep fake Blob, fake page counter, fake ingest runner, fake clock, and fake ID generator deterministic.
5. Update [../ingest/PDF_UPLOAD_FLOW.md](../ingest/PDF_UPLOAD_FLOW.md) and [../ingest/TDD_PROMPTS.md](../ingest/TDD_PROMPTS.md).

Useful commands:

```bash
go test ./internal/upload/... -v
go test ./internal/api/... -run 'Upload|Chunk' -v
go test ./internal/api/... -run UploadWorkflow -v
```

---

## Adding RAG or Ingest Behavior

RAG and ingest changes have a higher blast radius because they affect answer quality and Azure cost.

Checklist:

- Add pure tests for parsing, chunking, filtering, and dry-run behavior before live Azure tests.
- Keep `DryRunReport` paths free of Azure calls.
- Make source typing explicit: `banner`, `banner_user_guide`, and `sop` should not bleed into each other.
- Check chunk IDs before changing chunk metadata. Existing chunks may depend on deterministic IDs.
- Update troubleshooting notes if new errors can reach operators.

Useful commands:

```bash
go test ./internal/ingest/... -v
go test ./internal/rag/... -v
go test ./internal/azure/... -v
```

---

## Adding Adapter Behavior

The adapter is intentionally thin. It classifies, routes, and normalizes responses; it does not run RAG
logic itself.

Checklist:

- Add tests in `api/` for HTTP behavior.
- Add tests in `internal/adapter` for backend client behavior using `httptest.NewServer`.
- Add tests in `internal/intent` or `internal/sentiment` for classifier changes.
- Do not hardcode a live backend URL in tests.

Useful commands:

```bash
go test ./api/... ./internal/adapter/... ./internal/intent/... ./internal/sentiment/... -v
```

---

## Environment Variables

The backend loads `.env` through `config.Load()`. Required for the backend:

- `AZURE_OPENAI_ENDPOINT`
- `AZURE_OPENAI_API_KEY`
- `AZURE_SEARCH_ENDPOINT`
- `AZURE_SEARCH_API_KEY`

Required for Phase U upload:

- `AZURE_STORAGE_CONNECTION_STRING`
- `AZURE_STORAGE_CONTAINER_NAME`

Required for URL upload:

- `UPLOAD_URL_ALLOWLIST` when using hosts outside the default allowlist.
- `MAX_UPLOAD_SIZE_MB` when the default upload limit is too small for expected PDFs.

Required for the adapter:

- `RAG_BACKEND_URL`

Never commit `.env`, Azure keys, Fly secrets, Botpress tokens, or ngrok tokens.

---

## Definition of Done

Before handing off a code change:

- Narrow tests for touched packages pass.
- Broader tests pass when practical: `go test ./... -v`.
- Generated files are refreshed only when their source changed.
- Docs are updated for endpoint, env var, response, workflow, or troubleshooting changes.
- `git diff --check` is clean for files you touched.
- Unrelated dirty files are left untouched.
