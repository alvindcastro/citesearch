# INGEST.md — Additions for PDF Upload Flow
#
# These sections belong in INGEST.md.
#
# Merge instructions:
#   1. Replace the Phase M endpoint table under
#      "Upload Workflow — Ingest Without Filesystem Access" with the updated table below.
#   2. Append "Partial Chunking and Sidecar State" as a new section after
#      "Upload Workflow — Ingest Without Filesystem Access".

---

## Updated Phase M Endpoint Table

Replace the existing table in "Upload Workflow — Ingest Without Filesystem Access" with:

| Path | Endpoint | Best for |
|---|---|---|
| Folder-based (existing) | `POST /banner/ingest` | Local dev, bulk initial ingest from disk |
| Azure Blob sync (existing) | `POST /banner/blob/sync` | Production, pre-populated Blob container |
| Upload — multipart (Phase M.2) | `POST /banner/upload` | Ad-hoc file upload to Blob. Creates sidecar. Does not chunk. |
| Upload — from URL (Phase M.2) | `POST /banner/upload/from-url` | Ellucian ECC links, automation. Downloads to Blob. Creates sidecar. Does not chunk. |
| Chunk (Phase M.3) | `POST /banner/upload/chunk` | Chunk a page range of any uploaded PDF. Can be called multiple times with non-overlapping ranges in any order. |
| Status (Phase M.3) | `GET /banner/upload/{id}/status` | Read sidecar: chunked ranges, unchunked ranges, chunking_pattern, gap_summary. |
| List (Phase M.3) | `GET /banner/upload` | List all tracked uploads with chunking status and gap summaries. |
| Delete (Phase M.3) | `DELETE /banner/upload/{id}` | Remove blob and sidecar. Pass `?purge_index=true` to also remove chunks. |

**Sidecar coverage:** both upload endpoints create a sidecar immediately on completion.
All Phase M.3 chunk, status, list, and delete operations work identically regardless of
which upload path was used.

---

## New Section: Partial Chunking and Sidecar State

Add this section after "Upload Workflow — Ingest Without Filesystem Access".

---

### Partial Chunking and Sidecar State

The Phase M upload path separates uploading from chunking. A PDF can be uploaded once and
chunked in multiple rounds across any page ranges, in any order. The system tracks chunking
progress in a **sidecar JSON blob** stored adjacent to the PDF in the same Blob container.

**Why this matters:**

- Large user guide PDFs (300-800 pages) take 30-120 minutes to fully chunk. Partial chunking
  lets you get the first 50-100 pages queryable in minutes while the rest indexes later.
- Operators can target specific chapters by page range rather than waiting for a full ingest.
- If a chunk run fails mid-way (rate limit, timeout, container restart), the sidecar records
  exactly which pages completed. Resuming picks up from the remaining gaps with no
  duplicates — chunk IDs are deterministic.
- The sidecar is the only persistent state required. No database, no queue, no background
  worker. The adapter reads and writes it on every chunk call.

### Sidecar location

For a PDF at blob path `banner/finance/releases/2026/Banner_Finance_9.3.22.pdf`, the sidecar
is at `banner/finance/releases/2026/Banner_Finance_9.3.22.pdf.chunks.json`.

Same container, same prefix, adjacent to the PDF. Both upload endpoints (`POST /banner/upload`
and `POST /banner/upload/from-url`) create the sidecar on completion. If the upload or
download fails, no sidecar is created.

### Sidecar schema

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
  "chunked_ranges": [
    {"start": 33, "end": 44, "chunked_at": "2026-04-26T10:05:00Z", "chunk_ids": [...]},
    {"start": 78, "end": 90, "chunked_at": "2026-04-26T10:22:00Z", "chunk_ids": [...]}
  ],
  "unchunked_ranges": [
    {"start": 1,  "end": 32},
    {"start": 45, "end": 77},
    {"start": 91, "end": 120}
  ],
  "status": "partial",
  "chunking_pattern": "sparse",
  "gap_count": 3,
  "gap_summary": "3 gaps: pages 1-32, 45-77, 91-120 (101 pages total unchunked)"
}
```

`unchunked_ranges` is always recomputed on write as the full complement of the union of all
`chunked_ranges` against `[1, total_pages]`. Caller-supplied values are ignored.

`chunking_pattern` values:

| Value | Meaning |
|---|---|
| `none` | No pages chunked yet. |
| `sequential` | All chunked ranges form one contiguous block starting at page 1. |
| `contiguous` | One contiguous block not starting at page 1. |
| `sparse` | Multiple non-contiguous chunked ranges with gaps between them. |

`gap_summary` is a human-readable string computed by the adapter on every write. Example:
`"3 gaps: pages 1-32, 45-77, 91-120 (101 pages total unchunked)"`.

### System layer vs. wizard layer

The system layer (Go adapter) is range-agnostic. `POST /banner/upload/chunk` accepts any
non-overlapping page range in any order — pages 33-44 before pages 1-32 is valid. The adapter
enforces only the overlap check and bounds check; it has no opinion on ordering.

The wizard layer (Agent 19 in `CLAUDE_AGENTS.md`) adds two modes:

- **Default mode:** enforces left-to-right contiguous chunking. The agent always uses
  `unchunked_ranges[0].start` as page_start and intercepts any operator request for an
  out-of-order range with a redirect prompt.
- **Advanced mode:** allows arbitrary ranges with explicit operator confirmation before each
  chunk call and a gap summary after. Entered only on explicit operator request.

This separation means direct API callers get full flexibility, while operators going through
the conversational interface get a safe, predictable experience by default.

### Overlap detection algorithm

The adapter rejects a new range `[page_start, page_end]` if for any existing
`[s, e]` in `chunked_ranges`:

```
page_start <= e AND page_end >= s
```

This catches full containment, partial overlap from either side, and exact duplicates. The
check is applied against all existing chunked ranges, not just the last one.

### Chunk-all-remaining with multiple gaps

When `POST /banner/upload/chunk` is called without `page_start`/`page_end`, the adapter
iterates all `unchunked_ranges` in ascending order, chunks each one sequentially, and writes
the sidecar after each gap completes. If the call fails mid-gap, the sidecar reflects the
completed gaps. Reissuing the same no-range call resumes from the next incomplete gap.

The response includes `gaps_processed` and `gaps_remaining` to show how many distinct
unchunked intervals were handled in that call.

### Queryability during partial ingest

Chunks are queryable in Azure Search immediately after each chunk call completes. A query
against a `partial` document returns results only from chunked pages. A `sparse` document
may return inconsistent results — content from pages 33-44 and 78-90 is findable, but
pages 1-32 and everything outside those ranges is not.

`gap_summary` in the status response is the diagnostic signal for operators who report
missing results from a partially indexed document.

### Relationship to the existing ingest pipeline

The chunk step calls the same `ingest.Run()` function used by `POST /banner/ingest`, with
the blob download replacing the filesystem path and `start_page`/`end_page` scoped to the
requested range. No changes to the ingest pipeline are required.

### Full operator reference

See [PDF_UPLOAD_FLOW.md](PDF_UPLOAD_FLOW.md) for the complete operator reference:
endpoint contracts, curl examples, sidecar schema, and the error reference.

See [CLAUDE_AGENTS.md](CLAUDE_AGENTS.md) § Agent 19 to drive this flow conversationally,
including default and advanced chunking modes.
