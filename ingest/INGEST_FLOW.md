# Ingest Flow — End-to-End Narrative

How a PDF goes from a file on disk or a URL into queryable chunks in Azure Search.
Covers all entry points, the sidecar state machine, partial and sparse chunking, and the
two-layer wizard interface.

This is a flow document. For endpoint contracts and curl examples see
[PDF_UPLOAD_FLOW.md](PDF_UPLOAD_FLOW.md). For the Go implementation see
[INTERNALS.md](INTERNALS.md) § Data Flow: Ingestion Pipeline.

---

## Overview

The ingest flow has two distinct generations:

**Generation 1 — filesystem-based (existing).** The operator drops PDFs into a folder on the
server and calls `POST /banner/ingest`. The handler walks the folder, extracts text, chunks,
embeds, and writes to Azure Search in one blocking call. Simple, but requires SSH access or a
local dev environment. Not usable from Fly.io or a remote Docker deployment.

**Generation 2 — upload-based (Phase M).** The operator sends the PDF to the server over
HTTP. Upload and chunking are separate steps. Uploading stores the PDF in Azure Blob Storage
and creates a sidecar tracking file. Chunking reads from Blob, runs the same pipeline as
Generation 1, and updates the sidecar. A single PDF can be chunked in multiple rounds across
any page ranges, in any order. The sidecar is the only persistent state — no database, no
queue.

Both generations write to the same Azure Search index and produce the same chunk shape. Once
a chunk is in the index, it is queryable regardless of which path produced it.

---

## Stage 0 — Entry Point Selection

Before anything starts, the entry point is determined by deployment context:

```text
File on local disk + local server running
    → POST /banner/ingest (Generation 1)

Blob Storage pre-populated + production deploy
    → POST /banner/blob/sync (Generation 1, Blob-triggered)

File on operator's machine + cloud/remote server
    → POST /banner/upload (Generation 2, multipart)

PDF at an HTTPS URL (e.g. Ellucian ECC)
    → POST /banner/upload/from-url (Generation 2, URL)

Returning to chunk a previously uploaded PDF
    → POST /banner/upload/chunk (Generation 2, chunk only)
```

For local dev, Generation 1 is simpler. For everything else, Generation 2.

---

## Stage 1 — Upload (Generation 2 only)

The upload step is fast — it is purely a file transfer plus sidecar creation. No embedding,
no Azure Search calls.

### Multipart upload

The operator posts the PDF as multipart/form-data with four metadata fields: `source_type`,
`module`, `version` (release notes only), and `year` (release notes only).

The handler:

1. Validates the fields. Rejects if `module` is unknown, `version`/`year` is present on a
   user guide, or the file is not a recognised extension.
2. Synthesises the blob path from metadata. `source_type=banner`, `module=Finance`,
   `year=2026` → `banner/finance/releases/2026/<filename>`. The path mirrors the
   `data/docs/` folder structure used by Generation 1.
3. Checks for an existing blob at that path. Returns 409 if one exists.
4. Writes the PDF to Blob Storage.
5. Calls `ingest.CountPages()` to extract the total page count from the PDF without chunking.
6. Assigns a UUID as `upload_id`.
7. Creates the initial sidecar at `{blob_path}.chunks.json` with `status=pending`,
   `chunked_ranges=[]`, `total_pages` from step 5.
8. Returns `upload_id`, `blob_path`, `total_pages`, `status=pending`.

At this point the PDF is in Blob Storage but nothing is in Azure Search.

### URL upload

`POST /banner/upload/from-url` takes a JSON body with `url` and the same metadata fields.

The handler does everything the multipart handler does, but inserts a download step before
the blob write:

1. Validates the URL is HTTPS and the hostname is on the `UPLOAD_URL_ALLOWLIST`.
2. Downloads the PDF with a 60-second timeout.
3. Validates the file extension after download.
4. Continues identically to the multipart handler from step 3 onward.

If the download fails (timeout, 404, 5xx), no blob is written and no sidecar is created.
The operator sees a descriptive error code and can retry or fall back to multipart upload.

The sidecar created by URL upload is identical in schema to one created by multipart upload.
All subsequent operations are indistinguishable between the two paths.

---

## Stage 2 — Sidecar Creation

The sidecar is a JSON blob stored adjacent to the PDF at `{blob_path}.chunks.json`. It is
written once at upload time and updated on every chunk call. It is the only persistent record
of chunking progress.

Initial sidecar state after upload:

```json
{
  "blob_path": "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
  "upload_id": "a3f8c1d2-...",
  "uploaded_at": "2026-04-26T10:00:00Z",
  "source_type": "banner",
  "module": "Finance",
  "version": "9.3.22",
  "year": "2026",
  "total_pages": 120,
  "chunked_ranges": [],
  "unchunked_ranges": [{"start": 1, "end": 120}],
  "status": "pending",
  "chunking_pattern": "none",
  "gap_count": 1,
  "gap_summary": "1 gap: pages 1-120 (120 pages total unchunked)"
}
```

`unchunked_ranges` is always the full complement of `chunked_ranges` against `[1, total_pages]`.
It is computed by the adapter on every write and never trusted from caller input. The initial
state has one gap covering the entire document.

---

## Stage 3 — Chunking

Chunking is triggered by `POST /banner/upload/chunk`. The request carries the `upload_id`
and an optional `page_start`/`page_end`. If neither is supplied, all remaining unchunked
pages are processed.

### Validation before any embedding starts

The handler reads the sidecar first. For a targeted range `[page_start, page_end]`, it runs
the overlap check before doing any work: reject if for any existing `[s, e]` in
`chunked_ranges`, `page_start <= e AND page_end >= s`. This catches full containment, partial
overlap from either side, and exact duplicates. A rejected range returns 400 immediately — no
Blob reads, no embedding calls.

### Chunk loop

For each range to process (one targeted range, or all `unchunked_ranges` in ascending order):

1. Download the relevant PDF pages from Blob Storage.
2. Call `ingest.Run()` scoped to `startPage`/`endPage`. This is the same pipeline as
   Generation 1: extract text → split into chunks → embed each chunk via Azure OpenAI →
   write to Azure Search using merge-or-upload semantics.
3. Collect the chunk IDs produced. Chunk IDs are deterministic:
   `MD5(blob_path + page + index)`. This means re-chunking the same page range produces the
   same IDs, and Azure Search's merge-or-upload replaces existing chunks rather than
   duplicating them. Re-running after a failure is safe.
4. Append to `chunked_ranges` with the timestamp and chunk IDs.
5. Recompute `unchunked_ranges` as the complement of the union of all `chunked_ranges`.
6. Update `chunking_pattern`, `gap_count`, `gap_summary`.
7. Write the sidecar atomically.

Step 7 happens after each gap completes. If the call processes multiple gaps and fails
mid-way, the sidecar reflects the completed gaps and the next call resumes from the
remaining ones.

### Chunking patterns

The `chunking_pattern` field reflects how the chunked ranges are arranged:

```text
none        → no pages chunked (status=pending)

sequential  → chunked_ranges = [{start:1, end:N}]
              One contiguous block starting from page 1.
              The normal left-to-right case.
              Queries have predictable coverage: "first N of M pages are searchable."

contiguous  → chunked_ranges = [{start:K, end:N}] where K > 1
              One contiguous block not starting at page 1.
              Pages 1 through K-1 are unchunked.

sparse      → chunked_ranges has two or more non-adjacent entries
              Gaps exist between chunked ranges.
              Queries may silently miss content from unchunked pages.
              gap_summary describes the exact gaps.
```

`sequential` is the normal production state for a fully or partially indexed document. `sparse`
occurs when pages are chunked out of order — either via direct API calls with arbitrary ranges,
or when the planned Agent 19 advanced mode is used.

### After the chunk call

The response returns the updated sidecar fields plus `gaps_processed` and `gaps_remaining`.
Chunks written in this call are queryable in Azure Search immediately — the index does not
have a "document complete" concept. Each chunk is an independent indexed document.

---

## Stage 4 — Status Tracking

`GET /banner/upload/{upload_id}/status` reads the sidecar and returns its full state plus two
computed fields:

- `queryable_page_count` — sum of all chunked page ranges.
- `estimated_remaining_minutes` — `ceil(remaining_pages * avg_chunks_per_page * 0.5s / 60)`.

`GET /banner/upload` lists all sidecars with a summary view: `upload_id`, `blob_path`,
`status`, `chunking_pattern`, `total_pages`, `queryable_page_count`, `gap_count`,
`gap_summary`.

Status values:

```text
pending   → sidecar created, zero pages chunked, not queryable
partial   → some pages chunked and queryable, gaps remain
complete  → all pages chunked, fully queryable
```

A document at `status=complete` is behaviourally identical to one ingested via
`POST /banner/ingest`. The chunking path used to get there is not visible to the query layer.

---

## Stage 5 — The Sidecar as a State Machine

The sidecar transitions through states as chunk calls land:

```text
[upload]
    │
    ▼
status=pending
chunking_pattern=none
chunked_ranges=[]
unchunked_ranges=[{1, total_pages}]
    │
    │  POST /banner/upload/chunk (page_start=1, page_end=50)
    ▼
status=partial
chunking_pattern=sequential
chunked_ranges=[{1,50}]
unchunked_ranges=[{51,120}]
    │
    │  POST /banner/upload/chunk (page_start=51, page_end=120)  ← left-to-right
    │                    OR
    │  POST /banner/upload/chunk (page_start=78, page_end=90)   ← out-of-order
    ▼
status=partial
chunking_pattern=sequential           chunking_pattern=sparse
chunked_ranges=[{1,120}]*             chunked_ranges=[{1,50},{78,90}]
unchunked_ranges=[]                   unchunked_ranges=[{51,77},{91,120}]
    │                                     │
    ▼                                     │  POST /banner/upload/chunk (no range)
status=complete                           │  fills all gaps in ascending order
chunking_pattern=sequential               ▼
                                      status=complete
                                      chunking_pattern=sequential

* if page_end=120 on a 120-page doc, unchunked_ranges becomes [] and status flips to complete
```

The state machine has no rollback — once pages are chunked they stay in the index until
explicitly deleted via `DELETE /banner/upload/{id}?purge_index=true`.

---

## Stage 6 — Queryability During Partial Ingest

Partial ingest has predictable and unpredictable queryability depending on the pattern:

**`sequential`:** predictable. Pages 1 through N are searchable. A user asking about content
on page N+1 gets no result — but at least the boundary is clear. `gap_summary` reports
"1 gap: pages N+1 through M."

**`sparse`:** unpredictable. Pages 33-44 and 78-90 are searchable; everything else is not.
A query answered by page 20 returns nothing with no indication that page 20 exists. This is
the most dangerous partial state from a user-trust perspective. `gap_summary` exposes the
exact gaps. Any operator facing user complaints about missing results should check
`chunking_pattern` first.

**`complete`:** fully queryable. No special handling needed at the query layer.

The query layer (`/banner/ask`, `/banner/{module}/ask`) has no awareness of chunking state.
It searches whatever is in the index. The `gap_summary` field is the only mechanism for
communicating coverage boundaries to operators and users.

---

## Stage 7 — The Wizard Layer (Planned Agent 19)

The system layer (Go adapter) is range-agnostic. Any non-overlapping page range in any order
is accepted. This is intentional — developers calling the API directly get full flexibility.

The wizard layer, planned as Agent 19, is the opinionated operator interface on top of the
system layer.
It operates in two modes:

### Default mode

Agent 19 will enforce left-to-right contiguous chunking. It always uses
`unchunked_ranges[0].start` as `page_start`. It only asks the operator for a `page_end` —
how many pages to index now.

If the operator asks for an out-of-order range ("chunk pages 78-90 first"), the agent
intercepts before making any tool call and offers two choices: extend the range back to start
at `unchunked_ranges[0].start`, or switch to advanced mode. It does not proceed without
explicit input.

In default mode, the operator never sees the words "gap," "sparse," or "non-contiguous."
The sidecar is always `sequential` or `complete` after a default-mode session.

### Advanced mode

Entered only when the operator explicitly requests it. On entry, the agent delivers a one-time
framing message explaining what arbitrary chunking means for queryability.

In advanced mode, every `chunk_pages` tool call is preceded by a confirmation:

```text
"Chunk pages {start}-{end} of {total_pages}.
 After this, unchunked pages will be: {plain list}.
 Confirm?"
```

After every call, the agent reports `gap_summary` verbatim and the current `chunking_pattern`
in plain language. If the pattern is `sparse`, it notes that queries will only find content
from the indexed pages and offers to fill all gaps at once.

Advanced mode exits when the operator says so. Gaps are not retroactively filled on exit.

### Why the split

The split exists because the operator audience is mixed. A Banner curriculum admin does not
need to think about page ranges, sidecar state, or sparse patterns. The default mode gives
them a safe, linear experience. A developer ingesting a 480-page user guide who only cares
about chapter 3 (pages 200-280) can get there in two steps via advanced mode, with a clear
explanation of what they are trading off.

The system layer being range-agnostic means this split is purely in the prompt and tool
call logic — no backend code enforces ordering. Any future interface (CLI, Botpress flow,
automation script) can choose its own policy on top of the same endpoints.

---

## Full Flow Diagram

```text
Operator has a PDF
        │
        ├─── local file, local server ──────────────► POST /banner/ingest
        │                                              (Generation 1, sync, blocking)
        │
        ├─── local file, cloud server ─────────────► POST /banner/upload (multipart)
        │                                              │
        └─── URL (ECC link), any server ────────────► POST /banner/upload/from-url
                                                       │
                                          ┌────────────┘
                                          ▼
                               [Blob Storage write]
                               [ingest.CountPages()]
                               [Sidecar created: status=pending]
                               [upload_id returned]
                                          │
                               ┌──────────┴──────────────────────┐
                               │                                  │
                        Agent 19 (planned)                 Direct API call
                        (wizard layer)                     (system layer)
                               │                                  │
                        default mode                    any non-overlapping
                        left-to-right only              range in any order
                               │                                  │
                               └──────────┬──────────────────────┘
                                          ▼
                               POST /banner/upload/chunk
                               {upload_id, page_start?, page_end?}
                                          │
                               [Read sidecar]
                               [Overlap check]
                               [Download PDF pages from Blob]
                               [ingest.Run(startPage, endPage)]
                                 ├─ extract text
                                 ├─ chunk (char-based or section-aware)
                                 ├─ embed (Azure OpenAI)
                                 └─ write to Azure Search (merge-or-upload)
                               [Append to chunked_ranges]
                               [Recompute unchunked_ranges]
                               [Update chunking_pattern, gap_summary]
                               [Write sidecar atomically]
                                          │
                               ┌──────────┴──────────────────────┐
                               │  gaps remaining?                 │  no gaps remaining
                               ▼                                  ▼
                        status=partial                     status=complete
                        Chunks queryable now               All chunks queryable
                        Resume later with                  Identical to Generation 1
                        same endpoint                      ingest result
                               │
                        GET /banner/upload/{id}/status
                        → chunked_ranges, unchunked_ranges,
                          chunking_pattern, gap_summary,
                          queryable_page_count,
                          estimated_remaining_minutes
```

---

## Related Docs

| Doc | What it adds |
|---|---|
| [INGEST.md](INGEST.md) | Full operator reference: folder structure, naming conventions, pre-ingest checklist, endpoint contracts, curl examples |
| [PDF_UPLOAD_FLOW.md](PDF_UPLOAD_FLOW.md) | Phase M endpoint reference: request/response shapes, sidecar schema, partial and non-contiguous workflow examples, error reference |
| [INTERNALS.md](INTERNALS.md) § Data Flow: Ingestion Pipeline | Go implementation: package layout, handler flows, sidecar computation algorithms |
| [CLAUDE_AGENTS.md](../wiki/CLAUDE_AGENTS.md) | Agent guidance; Agent 19 still needs to be added for the upload chunking wizard |
| [TROUBLESHOOTING.md](../wiki/TROUBLESHOOTING.md) | General troubleshooting; Phase M upload/chunk errors still need a dedicated section |
