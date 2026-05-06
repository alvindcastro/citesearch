# Testing Guide

How to choose and run the right tests for citesearch.

---

## Quick Commands

| Scope | Command |
|---|---|
| Everything | `go test ./... -v` |
| Everything with race detector | `go test ./... -v -race` |
| Backend handlers | `go test ./internal/api/... -v` |
| Upload services | `go test ./internal/upload/... -v` |
| Upload route workflow | `go test ./internal/api/... -run UploadWorkflow -v` |
| Ingest parsing and dry-run | `go test ./internal/ingest/... -v` |
| Chatbot adapter | `go test ./api/... ./internal/adapter/... -v` |
| Intent and sentiment | `go test ./internal/intent/... ./internal/sentiment/... -v` |
| gRPC server | `go test ./internal/grpcserver/... ./cmd/grpc/... -v` |
| Azure wrappers | `go test ./internal/azure/... -v` |

Use `-count=1` when you need to avoid cached results:

```bash
go test ./internal/api/... -run UploadWorkflow -v -count=1
```

---

## Which Tests Should I Run?

| Change type | Minimum local check |
|---|---|
| Docs only | `git diff --check -- <changed-files>` |
| Upload metadata, ranges, or sidecars | `go test ./internal/upload/... -v` |
| Upload HTTP route | `go test ./internal/api/... -run 'Upload|Chunk' -v` |
| End-to-end upload workflow | `go test ./internal/api/... -run UploadWorkflow -v` |
| Ingest parsing or chunking | `go test ./internal/ingest/... -v` |
| RAG retrieval, confidence, or prompt flow | `go test ./internal/rag/... -v` |
| Adapter routing or response normalization | `go test ./api/... ./internal/adapter/... ./internal/intent/... -v` |
| gRPC API | `go test ./internal/grpcserver/... ./cmd/grpc/... -v` |

Run `go test ./... -v` before handoff when the change crosses package boundaries or touches
shared behavior.

---

## Test Categories

### Unit Tests

Most tests are pure or use fakes. They must not require live Azure credentials.

Examples:

- `internal/upload/range_math_test.go`
- `internal/upload/metadata_test.go`
- `internal/ingest/docx_test.go`
- `internal/intent/classifier_test.go`
- `internal/sentiment/analyzer_test.go`

### Handler Tests

Handler tests use `httptest` and injected collaborators. They should verify:

- HTTP status codes.
- JSON response shape.
- Validation failures.
- Which collaborator was called.
- Which collaborator was not called.

Examples:

- `internal/api/upload_handler_test.go`
- `internal/api/upload_chunk_handler_test.go`
- `api/handlers_test.go`

### Offline Workflow Tests

`internal/api/upload_workflow_test.go` covers full upload route workflows with fake dependencies.
It proves operator flows without live Azure, public network access, Azure OpenAI, or Azure Search.

```bash
go test ./internal/api/... -run UploadWorkflow -v
```

Covered flows:

- Multipart upload -> status -> chunk -> status -> list -> delete.
- URL upload -> status -> chunk all remaining.
- Sparse partial chunking and gap summaries.
- Upload, status, and list do not index before chunk.

### Live Integration Tests

There are no default live Azure integration tests. If you add one, make it opt-in:

- Use separate `.env.test` or explicit env vars.
- Skip when credentials are not present.
- Use a disposable Azure AI Search index.
- Never run live tests as part of ordinary `go test ./...`.

Pattern:

```go
if os.Getenv("CITESEARCH_LIVE_AZURE") != "1" {
    t.Skip("set CITESEARCH_LIVE_AZURE=1 to run live Azure integration test")
}
```

---

## Recommended Workflow for Changes

1. Run the package test that covers the change.
2. Run adjacent package tests if the change crosses a boundary.
3. Run `go test ./... -v` before handoff when practical.
4. For docs-only changes, run `git diff --check -- <changed-files>`.

Examples:

```bash
# Upload handler change
go test ./internal/upload/... -v
go test ./internal/api/... -run 'Upload|Chunk' -v
go test ./internal/api/... -run UploadWorkflow -v

# Adapter routing change
go test ./api/... ./internal/adapter/... ./internal/intent/... -v

# Ingest parser change
go test ./internal/ingest/... -v
```

---

## Race Detector Notes

Use the race detector when changing shared state, locks, handler globals, goroutines, or background
work:

```bash
go test ./... -v -race
```

On Windows, the race detector requires `CGO_ENABLED=1`. If your shell cannot run it, run the
functional suite without `-race` and call out that race coverage was not run locally.

---

## Coverage Notes

This repo values focused behavioral tests over raw coverage percentage. Add broader tests when:

- A change touches shared route wiring.
- A change modifies sidecar state transitions.
- A change modifies query routing or confidence behavior.
- A change can silently index the wrong source type.
- A change can increase Azure cost or latency.

---

## Troubleshooting Tests

| Symptom | Likely Cause | Fix |
|---|---|---|
| Tests use stale behavior | Go test cache | Add `-count=1`. |
| Race detector unavailable | CGO disabled or unsupported shell | Drop `-race` locally, run on Linux when possible. |
| Handler tests hit localhost | Test bypassed fake client | Use `httptest.NewServer` or injected fake dependencies. |
| Upload workflow tests fail after response change | JSON shape changed | Update tests and docs together. |
| Azure tests fail in ordinary suite | Live dependency leaked into unit test | Add interface seam, fake dependency, or opt-in skip. |
| `go test ./...` is slow | Broad suite is doing too much for the current edit | Run the owning package first, then broaden before handoff. |

---

## Pre-Handoff Checklist

- `go test ./... -v` passes, or skipped scope is explicitly called out.
- `git diff --check -- <changed-files>` passes.
- New endpoints have handler tests.
- New upload workflows have offline workflow coverage.
- New env vars are documented in README or the relevant wiki page.
- New failure modes are documented in [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
