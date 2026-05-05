# Upload-Based Ingest TDD Prompt Plan

This file turns the `/ingest` design docs into implementation prompts. It is planning material
only: do not implement code from this file until a phase is explicitly selected.

Canonical source for this plan:
- [INGEST_FLOW.md](INGEST_FLOW.md)
- [INGEST.md](INGEST.md)
- [PDF_UPLOAD_FLOW.md](PDF_UPLOAD_FLOW.md)
- [UPLOAD_SPEC_DECISION.md](UPLOAD_SPEC_DECISION.md)
- [INTERNALS.md](INTERNALS.md)

Important design note: the `/ingest` docs describe the newer decoupled upload model. Uploading
stores the document in Azure Blob Storage and creates a sidecar. Chunking is a later call that
updates the sidecar. This differs from the older `PLAN.md` Phase M sketch where upload writes a
local file and immediately calls `ingest.Run()`. For the phases below, follow the `/ingest`
Blob-plus-sidecar model.

## TDD Rules for Every Phase

- [ ] Start by writing failing tests that name the expected behavior.
- [ ] Keep Azure, HTTP download, filesystem, and ingest calls behind testable seams before
      implementing handlers that depend on them.
- [ ] Use fake clients in unit tests. Do not require live Azure credentials for ordinary tests.
- [ ] Keep handler tests focused on status codes, JSON shape, validation, and which collaborator
      methods are called.
- [ ] Run the narrow package tests first, then the broader package tests after the phase is green.
- [ ] Update docs in the same phase when an endpoint, env var, response, or operator workflow changes.
- [ ] Do not commit. Suggest a commit message at the end of each implementation phase.

## Phase U.0 - Reconcile Upload Spec Before Coding

- [x] Confirm that `/ingest/PDF_UPLOAD_FLOW.md` is the canonical behavior for upload, chunk, status,
      list, and delete.
- [x] Confirm that upload endpoints do not call Azure OpenAI or Azure AI Search.
- [x] Confirm that chunk endpoints are the only upload-flow endpoints that call the ingest pipeline.
- [x] Confirm whether `DOCX`, `TXT`, and `MD` are in scope for upload now or whether Phase U should
      start with PDF only despite older tables mentioning multiple extensions.
      Decision: Phase U upload supports PDFs only. DOCX, TXT, MD, and SOP uploads are out of
      scope and must be rejected.
- [x] Confirm whether index purge on delete must be implemented in the first delete phase or deferred.

Prompt for implementer:

```text
Read ingest/INGEST_FLOW.md, ingest/INGEST.md, ingest/PDF_UPLOAD_FLOW.md, ingest/INTERNALS.md,
and the current PLAN.md Phase M section. Do not code. Produce a short decision note that names
the canonical upload design, lists any conflicting requirements, and updates the implementation
prompt docs if needed.
```

Acceptance criteria:

- [x] The canonical upload model is stated in writing.
- [x] Any conflict with `PLAN.md` is called out before implementation starts.
- [x] The next implementation phase can proceed without guessing about upload-vs-chunk behavior.
- [x] Downstream implementation phases reflect PDF-only scope.

## Phase U.1 - Test Seams and Upload Package Skeleton

- [x] Add an `internal/upload` package without production behavior beyond types and interfaces.
- [x] Define seams for Blob storage, page counting, ingest execution, clock, UUID generation, and
      HTTP download.
- [x] Keep Azure SDK types out of handler tests.
- [x] Keep sidecar JSON structs in the upload package, not in `internal/api`.

Prompt for implementer:

```text
Strict TDD. Add the smallest upload package skeleton needed to test Phase U behavior. Start with
tests that compile only after you introduce interfaces and core structs: SidecarState,
ChunkRange, BlobStore, PageCounter, IngestRunner, Clock, and IDGenerator. Do not implement endpoint
logic yet. Keep the public surface small and avoid Azure SDK dependencies in tests.
```

Red tests:

- [x] `TestUploadPackage_ExposesSidecarStateJSONShape`
- [x] `TestUploadPackage_AllowsFakeBlobStore`
- [x] `TestUploadPackage_AllowsFakeIngestRunner`
- [x] `TestUploadPackage_ServiceKeepsDependenciesExplicit`

Green tasks:

- [x] Create `internal/upload` package.
- [x] Add sidecar/request/response structs with JSON tags matching `PDF_UPLOAD_FLOW.md`.
- [x] Add interfaces needed by future phases.

Refactor tasks:

- [x] Move any generic page-range structs into one file.
- [x] Keep constructor defaults explicit so handlers can wire real dependencies later.

Acceptance criteria:

- [x] `go test ./internal/upload/... -v` passes.
- [x] No handler routes are added yet.
- [x] No live Azure dependency is required.

## Phase U.2 - Sidecar Range Math

- [x] Implement unchunked-range computation from `total_pages` and `chunked_ranges`.
- [x] Implement overlap detection.
- [x] Implement chunking-pattern classification: `none`, `sequential`, `contiguous`, `sparse`.
- [x] Implement `status`, `gap_count`, `gap_summary`, `queryable_page_count`, and
      `remaining_page_count` derivation.

Prompt for implementer:

```text
Strict TDD. Implement the pure sidecar range functions described in ingest/PDF_UPLOAD_FLOW.md.
Write table-driven tests before code. Cover empty, sequential, contiguous-not-from-page-1, sparse,
adjacent ranges, out-of-order ranges, and invalid ranges. Do not add HTTP handlers in this phase.
```

Red tests:

- [x] `TestComputeUnchunkedRanges_NoneChunked`
- [x] `TestComputeUnchunkedRanges_SequentialPrefix`
- [x] `TestComputeUnchunkedRanges_ContiguousMiddle`
- [x] `TestComputeUnchunkedRanges_Sparse`
- [x] `TestCheckOverlap_DetectsContainmentPartialAndDuplicate`
- [x] `TestComputePattern_NoneSequentialContiguousSparse`
- [x] `TestGapSummary_FormatsSingleMultipleAndFullyIndexed`

Green tasks:

- [x] Sort and merge ranges defensively before computing gaps.
- [x] Reject invalid requested ranges where `start < 1`, `end < start`, or `end > total_pages`.
- [x] Recompute all derived fields from source state instead of trusting caller-supplied values.

Refactor tasks:

- [x] Keep range math pure and independent from Blob, Gin, and Azure Search.
- [x] Use table-driven tests for readability.

Acceptance criteria:

- [x] `go test ./internal/upload/... -run 'Range|Overlap|Pattern|Gap' -v` passes.
- [x] The examples in `PDF_UPLOAD_FLOW.md` are represented by tests.

## Phase U.3 - Blob Primitives and Sidecar Persistence

- [x] Add single-blob upload, download, exists, delete, read JSON, and write JSON operations.
- [x] Preserve existing blob sync behavior.
- [x] Store sidecars at `{blob_path}.chunks.json`.
- [x] Ensure supported-document listing does not accidentally treat sidecar JSON as ingestable input.

Prompt for implementer:

```text
Strict TDD. Extend the Blob storage layer behind an interface first, then add real Azure Blob
methods. Write fake-backed upload package tests before touching Azure SDK code. Existing
BlobList/BlobSync behavior must remain compatible.
```

Red tests:

- [x] `TestSidecarPath_AppendsChunksJSON`
- [x] `TestWriteSidecar_RoundTripsJSON`
- [x] `TestListUploads_IgnoresNonSidecarBlobs`
- [x] `TestListDocuments_IgnoresSidecarJSON`
- [x] `TestBlobUpload_UsesProvidedBlobPath`

Green tasks:

- [x] Add sidecar path helper.
- [x] Add real Blob methods needed by upload flow.
- [x] Add fake Blob store for upload package tests.
- [x] Update list filtering so `.chunks.json` is not treated as a document.

Refactor tasks:

- [x] Keep Azure SDK specifics in `internal/azure`.
- [x] Keep upload orchestration in `internal/upload`.

Acceptance criteria:

- [x] `go test ./internal/upload/... -v` passes.
- [x] `go test ./internal/azure/... -v` passes without live Azure credentials.
- [x] Existing `/banner/blob/list` and `/banner/blob/sync` contracts are unchanged.

## Phase U.4 - Count Pages and Upload Validation

- [x] Add `ingest.CountPages(filePath string) (int, error)` for PDFs.
- [x] Validate Phase U source types, module, version, year, PDF extension, and size limits.
- [x] Reject `source_type=sop`, `.docx`, `.txt`, and `.md` before any Blob write or sidecar
      creation.
- [x] Synthesize Blob paths from metadata using the `/ingest` path convention.
- [x] Keep user guides unversioned and release notes year-aware.

Prompt for implementer:

```text
Strict TDD. Add upload metadata validation, Blob path synthesis, and PDF page counting. Start with
pure unit tests for path and validation rules. Add CountPages tests using small fixtures or existing
testdata if available. Do not add upload HTTP handlers yet.
```

Red tests:

- [x] `TestSynthesizeBlobPath_BannerReleaseWithYear`
- [x] `TestSynthesizeBlobPath_BannerUserGuide`
- [x] `TestValidateUploadMetadata_RejectsMissingSourceType`
- [x] `TestValidateUploadMetadata_RejectsMissingModuleForBanner`
- [x] `TestValidateUploadMetadata_RejectsVersionOrYearForUserGuide`
- [x] `TestValidateUploadMetadata_RejectsUnknownModule`
- [x] `TestValidateUploadMetadata_RejectsUnsupportedExtension`
- [x] `TestValidateUploadMetadata_RejectsSOPUploadInPhaseU`
- [x] `TestValidateUploadMetadata_RejectsNonPDFUploadExtension`
- [x] `TestCountPages_ReturnsPDFPageCount`

Green tasks:

- [x] Add Blob path synthesis helper.
- [x] Add validation helper with typed errors or stable error codes for handlers.
- [x] Add `CountPages` using the existing PDF reader.
- [x] Add `MAX_UPLOAD_SIZE_MB` config with default `100`.

Refactor tasks:

- [x] Reuse existing module normalization where possible.
- [x] Keep operator-facing validation messages stable for tests and docs.

Acceptance criteria:

- [x] `go test ./internal/upload/... -v` passes.
- [x] `go test ./internal/ingest/... -run CountPages -v` passes.
- [x] `config.Config` documents `MAX_UPLOAD_SIZE_MB`.

## Phase U.5 - Multipart Upload Endpoint

- [x] Add `POST /banner/upload`.
- [x] Store uploaded PDF in Blob Storage.
- [x] Create initial sidecar with `status=pending`, `chunking_pattern=none`, and one full-page gap.
- [x] Return upload metadata and do not chunk.

Prompt for implementer:

```text
Strict TDD. Implement POST /banner/upload as a multipart endpoint. Tests must prove that a valid
upload writes the blob, counts pages, creates the sidecar, and does not call the ingest runner.
Use fakes for BlobStore, PageCounter, IDGenerator, and Clock. Wire the Gin route only after handler
tests fail for the missing route. Phase U upload is PDF-only; tests must prove non-PDF files and
`source_type=sop` are rejected before any Blob write or sidecar creation.
```

Red tests:

- [x] `TestBannerUpload_MissingSourceType_Returns400`
- [x] `TestBannerUpload_MissingModuleForBanner_Returns400`
- [x] `TestBannerUpload_UnsupportedExtension_Returns400`
- [x] `TestBannerUpload_SOPSourceType_Returns400`
- [x] `TestBannerUpload_FileTooLarge_Returns413`
- [x] `TestBannerUpload_DuplicateBlob_Returns409`
- [x] `TestBannerUpload_CreatesBlobAndInitialSidecar`
- [x] `TestBannerUpload_DoesNotCallIngest`
- [x] `TestBannerUpload_RouteIsRegistered`

Green tasks:

- [x] Add upload handler and route.
- [x] Stream multipart file to Blob or bounded temp file as needed by `CountPages`.
- [x] Write sidecar only after blob write and page count succeed.
- [x] Return response shape from `PDF_UPLOAD_FLOW.md`.

Refactor tasks:

- [x] Keep multipart parsing isolated from upload service logic.
- [x] Delete temp files after upload/page counting.

Acceptance criteria:

- [x] `go test ./internal/api/... -run BannerUpload -v` passes.
- [x] `go test ./internal/upload/... -v` passes.
- [x] Response includes `upload_id`, `blob_path`, `total_pages`, `status`, `chunking_pattern`,
      `gap_count`, `gap_summary`, and `message`.

## Phase U.6 - URL Upload Endpoint with SSRF Protection

- [x] Add `POST /banner/upload/from-url`.
- [x] Enforce HTTPS.
- [x] Enforce hostname allowlist with default Ellucian domains.
- [x] Enforce timeout and max downloaded size.
- [x] Create the same Blob and sidecar state as multipart upload.

Prompt for implementer:

```text
Strict TDD. Implement URL-based upload with SSRF protection. Start with table-driven tests for URL
allowlist behavior, then handler tests using httptest servers and a fake downloader where possible.
No Azure or public network calls are allowed in unit tests. Phase U upload is PDF-only; tests must
prove non-PDF downloads and `source_type=sop` are rejected before any Blob write or sidecar creation.
```

Red tests:

- [x] `TestIsAllowedURL_RejectsHTTP`
- [x] `TestIsAllowedURL_RejectsDisallowedHostname`
- [x] `TestIsAllowedURL_AllowsDefaultEllucianDomains`
- [x] `TestIsAllowedURL_AllowsWildcardOnlyForHTTPS`
- [x] `TestBannerUploadFromURL_Remote404Returns404`
- [x] `TestBannerUploadFromURL_Remote5xxReturns502`
- [x] `TestBannerUploadFromURL_TimeoutReturns408`
- [x] `TestBannerUploadFromURL_TooLargeReturns413`
- [x] `TestBannerUploadFromURL_NonPDFDownloadReturns400`
- [x] `TestBannerUploadFromURL_SOPSourceTypeReturns400`
- [x] `TestBannerUploadFromURL_CreatesBlobAndSidecar`
- [x] `TestBannerUploadFromURL_DoesNotCallIngest`

Green tasks:

- [x] Add `UPLOAD_URL_ALLOWLIST` config with default `customercare.ellucian.com,ellucian.com`.
- [x] Add bounded download helper.
- [x] Reuse upload service from multipart path after download.
- [x] Validate final extension from downloaded filename/path.

Refactor tasks:

- [x] Keep SSRF validation pure and table-tested.
- [x] Keep remote status mapping documented and stable.

Acceptance criteria:

- [x] `go test ./internal/api/... -run 'UploadFromURL|AllowedURL' -v` passes.
- [x] No handler test uses the public internet.
- [x] Failed downloads do not create sidecars.

## Phase U.7 - Chunk Uploaded Pages

- [ ] Add `POST /banner/upload/chunk`.
- [ ] Read sidecar by `upload_id`.
- [ ] Reject overlapping or out-of-bounds ranges.
- [ ] If no page range is provided, process all unchunked gaps in ascending order.
- [ ] Call the ingest pipeline only for requested gaps.
- [ ] Write sidecar after each completed gap.

Prompt for implementer:

```text
Strict TDD. Implement uploaded-document chunking. Start with service tests that use fake BlobStore
and fake IngestRunner, then add handler route tests. Prove that targeted ranges update sidecar
correctly, all-remaining mode processes multiple gaps in order, and overlap/out-of-bounds requests
return 400.
```

Red tests:

- [ ] `TestChunkUpload_NotFoundReturns404`
- [ ] `TestChunkUpload_OverlapReturns400`
- [ ] `TestChunkUpload_OutOfBoundsReturns400`
- [ ] `TestChunkUpload_TargetedRangeCallsIngestWithStartEnd`
- [ ] `TestChunkUpload_AllRemainingProcessesGapsInOrder`
- [ ] `TestChunkUpload_WritesSidecarAfterEachGap`
- [ ] `TestChunkUpload_FailureMidwayPreservesCompletedGaps`
- [ ] `TestChunkUpload_ResponseIncludesGapCountsAndStatus`

Green tasks:

- [ ] Resolve `upload_id` to sidecar and blob path.
- [ ] Download the source document to a temp path or stream it through the existing ingest path.
- [ ] Use existing `ingest.Run()` page range support for PDFs.
- [ ] Append chunked ranges with returned chunk IDs if available; otherwise document the limitation
      and add a follow-up phase to return IDs from ingest.

Refactor tasks:

- [ ] Keep chunk orchestration independent from Gin.
- [ ] Make sidecar writes atomic from the upload service perspective.

Acceptance criteria:

- [ ] `go test ./internal/upload/... -run Chunk -v` passes.
- [ ] `go test ./internal/api/... -run ChunkUpload -v` passes.
- [ ] `POST /banner/upload/chunk` is the only upload-flow endpoint that calls the ingest runner.

## Phase U.8 - Chunk Concurrency Guard

- [ ] Return 409 when a chunk run is already active for an `upload_id`.
- [ ] Keep the first implementation process-local unless Blob lease support is chosen explicitly.
- [ ] Ensure locks are released on success, validation failure, and ingest failure.

Prompt for implementer:

```text
Strict TDD. Add a per-upload chunk concurrency guard. Write tests that start one chunk operation,
attempt a second for the same upload_id, and assert 409. Also prove a different upload_id is not
blocked and that locks are released after failure.
```

Red tests:

- [ ] `TestChunkLock_SameUploadIDReturns409`
- [ ] `TestChunkLock_DifferentUploadIDAllowed`
- [ ] `TestChunkLock_ReleasedAfterSuccess`
- [ ] `TestChunkLock_ReleasedAfterFailure`

Green tasks:

- [ ] Add small lock manager in upload package.
- [ ] Wire lock acquisition around chunk execution.
- [ ] Map active-lock errors to HTTP 409.

Refactor tasks:

- [ ] Keep the lock manager replaceable if Blob leases are added later.

Acceptance criteria:

- [ ] `go test ./internal/upload/... -run Lock -v` passes.
- [ ] `go test ./internal/api/... -run ChunkUpload -v` passes.

## Phase U.9 - Status and List Endpoints

- [ ] Add `GET /banner/upload/{upload_id}/status`.
- [ ] Add `GET /banner/upload`.
- [ ] Include derived sidecar fields and estimates in responses.
- [ ] Do not modify sidecars during status reads.

Prompt for implementer:

```text
Strict TDD. Add status and list endpoints for uploaded documents. Tests should use fake sidecars
and assert response shape, derived fields, sorting, and 404 behavior. Reads must not mutate stored
sidecars.
```

Red tests:

- [ ] `TestUploadStatus_NotFoundReturns404`
- [ ] `TestUploadStatus_ReturnsDerivedFields`
- [ ] `TestUploadStatus_DoesNotWriteSidecar`
- [ ] `TestUploadList_ReturnsAllSidecarSummaries`
- [ ] `TestUploadList_SortsByUploadedAtDescending`
- [ ] `TestUploadList_EmptyReturnsEmptyArray`

Green tasks:

- [ ] Implement status service method.
- [ ] Implement list service method using sidecar discovery.
- [ ] Add routes.
- [ ] Add remaining-time estimate from unchunked page count.

Refactor tasks:

- [ ] Share response projection code between status, list, upload, and chunk responses.

Acceptance criteria:

- [ ] `go test ./internal/api/... -run 'UploadStatus|UploadList' -v` passes.
- [ ] Status/list responses match `PDF_UPLOAD_FLOW.md`.

## Phase U.10 - Delete Uploaded Document

- [ ] Add `DELETE /banner/upload/{upload_id}`.
- [ ] Delete the document blob and sidecar.
- [ ] Defer `?purge_index=true` until chunk IDs are reliably returned or persisted.
- [ ] Return explicit booleans for blob, sidecar, and chunk purge.

Prompt for implementer:

```text
Strict TDD. Implement uploaded-document delete. Start with deletion without index purge, then add
purge in a later phase only if the search layer has a tested delete-by-ID or delete-by-filter seam.
Do not fake a successful purge in production responses.
```

Red tests:

- [ ] `TestUploadDelete_NotFoundReturns404`
- [ ] `TestUploadDelete_DeletesBlobAndSidecar`
- [ ] `TestUploadDelete_DoesNotCallSearchByDefault`
- [ ] `TestUploadDelete_PurgeTrueReturnsNotImplementedUntilChunkIDsPersisted`

Green tasks:

- [ ] Implement delete service method.
- [ ] Add route.
- [ ] Leave search purge unimplemented until a tested chunk-ID persistence path exists.
- [ ] Map partial-delete errors clearly.

Refactor tasks:

- [ ] Keep purge behavior explicit in docs and responses.

Acceptance criteria:

- [ ] `go test ./internal/api/... -run UploadDelete -v` passes.
- [ ] Delete behavior matches documented response fields.

## Phase U.11 - Operator and Agent Documentation

- [ ] Update `wiki/INGEST.md` if endpoint behavior changed during implementation.
- [ ] Update `wiki/INTERNALS.md` for new package responsibilities and sidecar limitations.
- [ ] Update `CLAUDE.md` with routes, env vars, and operational constraints.
- [ ] Add or update Claude Agent guidance for upload coordination and chunking.
- [ ] Update API collection files if this repo treats them as maintained artifacts.
- [ ] Reconcile Agent 18 guidance with the decoupled upload model; it must not promise immediate
      queryability after upload unless a chunk call has completed.
- [ ] Decide whether Agent 19 is the chunking wizard, the upload coordinator, or both.
- [ ] Fix any stale examples discovered during implementation, including malformed curl URLs and
      ingest time-estimate inconsistencies.

Prompt for implementer:

```text
Strict docs verification. Compare implemented endpoint behavior against ingest/PDF_UPLOAD_FLOW.md,
wiki/INGEST.md, wiki/INTERNALS.md, CLAUDE.md, and wiki/CLAUDE_AGENTS.md. Patch only mismatches.
Do not broaden documented behavior beyond what tests prove.
```

Doc tasks:

- [ ] Document `POST /banner/upload`.
- [ ] Document `POST /banner/upload/from-url`.
- [ ] Document `POST /banner/upload/chunk`.
- [ ] Document `GET /banner/upload/{upload_id}/status`.
- [ ] Document `GET /banner/upload`.
- [ ] Document `DELETE /banner/upload/{upload_id}`.
- [ ] Document `MAX_UPLOAD_SIZE_MB`.
- [ ] Document `UPLOAD_URL_ALLOWLIST`.
- [ ] Document sidecar failure and recovery behavior.
- [ ] Document that upload is not queryable until chunking runs.
- [ ] Document consistently that Phase U upload accepts PDFs only and does not support
      DOCX/TXT/MD/SOP upload.

Acceptance criteria:

- [ ] Docs match implemented tests.
- [ ] Agent instructions do not instruct the adapter to perform backend-only operations.
- [ ] `git diff --check -- '*.md'` is clean.

## Phase U.12 - Integration Test Harness

- [ ] Add integration-style tests with fake Blob, fake ingest, fake clock, and fake UUID.
- [ ] Cover multipart upload -> status -> chunk -> status -> list -> delete.
- [ ] Cover URL upload -> status -> chunk all remaining.
- [ ] Keep tests offline and deterministic.

Prompt for implementer:

```text
Strict TDD. Add an offline integration harness around the upload routes. Use httptest and fake
dependencies. Prove the operator workflows in ingest/PDF_UPLOAD_FLOW.md without live Azure,
public network access, or real OpenAI/Search calls.
```

Red tests:

- [ ] `TestUploadWorkflow_MultipartStatusChunkListDelete`
- [ ] `TestUploadWorkflow_FromURLStatusChunkAllRemaining`
- [ ] `TestUploadWorkflow_PartialSparseStatusExplainsGaps`
- [ ] `TestUploadWorkflow_UploadNeverIndexesBeforeChunk`

Green tasks:

- [ ] Add route-level test harness for injected upload dependencies.
- [ ] Reuse fake clients from unit tests.
- [ ] Assert JSON response shapes with stable fixtures.

Refactor tasks:

- [ ] Remove duplicate fake setup from individual handler tests if the harness makes it clearer.

Acceptance criteria:

- [ ] `go test ./internal/api/... -run UploadWorkflow -v` passes.
- [ ] `go test ./internal/upload/... -v` passes.
- [ ] The full upload flow is covered without Azure credentials.

## Open Questions Before Implementation

- [x] Should Phase U support only PDFs at first, or include `.docx`, `.txt`, and `.md` from day one?
      Decision: PDF-only for Phase U; non-PDF upload is deferred.
- [ ] Should uploaded user guides reject `year` and `version` at validation time, as the docs say?
- [ ] Should sidecar writes use Azure Blob leases for cross-instance concurrency, or is process-local
      locking acceptable for the first pass?
- [x] Should chunk IDs be returned from `ingest.Run()` so sidecars can support exact index purge?
      Decision: exact index purge is deferred until chunk IDs are reliably returned or persisted.
- [ ] Should `GET /banner/upload` list only sidecars, or also detect orphan PDFs with missing sidecars?
- [ ] Should Blob paths include `AZURE_STORAGE_BLOB_PREFIX`, and if yes, should responses show prefixed
      or canonical unprefixed paths?
