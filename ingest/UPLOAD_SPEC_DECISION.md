# Upload Spec Decision — Phase U.0

Date: 2026-05-05

Phase U locks the upload-based ingest contract before code implementation begins.

## Decision

Phase U follows the `/ingest` Blob-plus-sidecar model:

- Upload endpoints write the PDF to Azure Blob Storage and create a sidecar.
- Upload endpoints do not call Azure OpenAI.
- Upload endpoints do not write to Azure Search.
- `POST /banner/upload/chunk` is the only upload-flow endpoint that calls the ingest pipeline.
- Sidecar state is the only persistent upload/chunk progress state.
- Chunked pages are queryable immediately after each chunk call completes.

`PLAN.md` Phase M is older planning material for an immediate local-write ingest flow. For Phase U
implementation, use the `/ingest` docs instead of the older `PLAN.md` upload handler sketch.

## Scope Locked for First Implementation

Phase U starts with PDF upload only:

- `source_type=banner`
- `source_type=banner_user_guide`
- extension `.pdf`
- page-based sidecar progress
- page-range chunking

The initial upload implementation does not include:

- SOP upload
- `.docx` upload
- `.txt` upload
- `.md` upload
- non-page-based chunk tracking

Reason: the upload flow is built around `ingest.CountPages()`, page ranges, sparse chunking, and
`queryable_page_count`. Those concepts are PDF-native in the current backend. Non-PDF upload needs
a separate progress model or a documented page-equivalent model before implementation.

## Delete and Index Purge

`DELETE /banner/upload/{upload_id}` should delete the PDF blob and sidecar in the first delete
implementation.

`?purge_index=true` is deferred until the ingest pipeline can reliably return or persist the exact
chunk IDs created for uploaded page ranges. The endpoint must not report a successful purge unless
the search layer has a tested delete path.

## Conflicts Resolved

- `PLAN.md` says upload writes a local file and immediately calls `ingest.Run()`. Phase U rejects
  that for upload endpoints and keeps chunking separate.
- Older upload tables mentioned `.docx`, `.txt`, `.md`, and SOP upload. Phase U rejects those for
  the first implementation and limits upload to PDFs.
- Agent 19 is planned, not currently implemented in `wiki/CLAUDE_AGENTS.md`; docs must describe it
  as planned until that agent spec exists.

## TDD Outcome

- Red: `/ingest` docs and `PLAN.md` disagreed on upload behavior, accepted file types, and index
  purge readiness.
- Green: the Phase U contract is now locked in this decision note and reflected in the prompt plan.
- Refactor: endpoint/operator docs were narrowed to PDF-only upload and deferred exact index purge.

## Next Phase

Proceed to Phase U.1: create test seams and an upload package skeleton. Do not implement endpoint
behavior until the skeleton and sidecar models are testable without live Azure dependencies.
