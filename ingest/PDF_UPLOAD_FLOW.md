# PDF Upload Flow — Operator Reference

Upload a PDF to Azure Blob Storage first, then chunk it into the search index in full or in
stages. Chunking and uploading are separate steps with explicit state tracked between them.

See [INGEST_FLOW.md](INGEST_FLOW.md) for the end-to-end narrative. See [INGEST.md](INGEST.md)
for the underlying ingest pipeline. Agent 19, the conversational wizard for this flow, still
needs to be added to [../wiki/CLAUDE_AGENTS.md](../wiki/CLAUDE_AGENTS.md).

---

## Table of Contents

1. [Why Upload and Chunk Are Separate](#why-upload-and-chunk-are-separate)
2. [Sidecar State File](#sidecar-state-file)
3. [Blob Path Convention](#blob-path-convention)
4. [Endpoints](#endpoints)
5. [Full Workflow (Happy Path)](#full-workflow-happy-path)
6. [URL Upload Workflow](#url-upload-workflow)
7. [Partial Chunking Workflow](#partial-chunking-workflow)
8. [Non-Contiguous Chunking](#non-contiguous-chunking)
9. [Resuming an Incomplete Chunk Run](#resuming-an-incomplete-chunk-run)
10. [Checking Status](#checking-status)
11. [Queryability During Partial Ingest](#queryability-during-partial-ingest)
12. [Pre-Upload Checklist](#pre-upload-checklist)
13. [Error Reference](#error-reference)

---

## Why Upload and Chunk Are Separate

The existing `POST /banner/ingest` pipeline is synchronous — it uploads, chunks, embeds, and
indexes in one blocking call. For large user guide PDFs (300+ pages, 60+ minutes), this is
impractical over HTTP.

The upload flow separates concerns:

- **Upload step**: writes the PDF to Azure Blob Storage and creates a sidecar tracking file.
  Fast — just a file transfer.
- **Chunk step**: reads from Blob, runs the ingest pipeline on a page range, and updates the
  sidecar. Can be called multiple times for the same PDF with non-overlapping ranges.

Once a page range is chunked, those chunks are immediately queryable in Azure Search. The rest
of the document can be chunked later — the system tracks exactly which pages are done.

---

## Sidecar State File

Every uploaded PDF has an adjacent sidecar blob at `{blob_path}.chunks.json`. The adapter
reads and writes this file on every chunk call. It is the only persistent state for the upload
flow.

### Schema

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
    {
      "start": 33,
      "end": 44,
      "chunked_at": "2026-04-26T10:05:00Z",
      "chunk_ids": ["abc123", "def456"]
    },
    {
      "start": 78,
      "end": 90,
      "chunked_at": "2026-04-26T10:22:00Z",
      "chunk_ids": ["ghi789", "jkl012"]
    }
  ],
  "unchunked_ranges": [
    {"start": 1,  "end": 32},
    {"start": 45, "end": 77},
    {"start": 91, "end": 120}
  ],
  "status": "partial",
  "chunking_pattern": "sparse",
  "gap_count": 3,
  "gap_summary": "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)"
}
```

### Fields

| Field | Set by | Description |
|---|---|---|
| `blob_path` | Upload | Blob path of the PDF. Unique — one PDF per path. |
| `upload_id` | Upload | UUID assigned at upload time. Use this to reference the PDF in subsequent calls. |
| `uploaded_at` | Upload | ISO 8601 timestamp of the upload. |
| `source_type` | Upload | `banner` or `banner_user_guide`. |
| `module` | Upload | Banner module name (e.g. `Finance`, `Student`, `General`). |
| `version` | Upload | Version string for release notes (e.g. `9.3.22`). Empty for user guides. |
| `year` | Upload | Year for release notes (e.g. `2026`). Empty for user guides. |
| `total_pages` | Upload | Total page count of the PDF, extracted immediately on upload. |
| `chunked_ranges` | Chunk | Sorted list of completed page ranges with timestamps and Azure Search chunk IDs. |
| `unchunked_ranges` | Chunk | Full complement of `chunked_ranges` against `[1, total_pages]`. Always recomputed on write. Never trusted from caller. |
| `status` | Chunk | `pending`, `partial`, or `complete`. |
| `chunking_pattern` | Chunk | `none`, `sequential`, `contiguous`, or `sparse`. See table below. |
| `gap_count` | Chunk | Number of distinct unchunked intervals. 0 when `status=complete`. |
| `gap_summary` | Chunk | Human-readable description of all unchunked ranges and total unchunked page count. Computed by adapter. |

### `chunking_pattern` values

| Value | Meaning |
|---|---|
| `none` | No pages chunked yet (`status=pending`). |
| `sequential` | All chunked ranges form one contiguous block starting at page 1. The common left-to-right case. |
| `contiguous` | One contiguous block that does not start at page 1 (e.g. pages 33-90 chunked, nothing before or after). |
| `sparse` | Two or more non-contiguous chunked ranges with gaps between them. Queries return results only from chunked pages. |

### Sidecar integrity rules

- `unchunked_ranges` is always recomputed from `total_pages` minus the union of all
  `chunked_ranges` intervals on every write. Caller-supplied values are never trusted.
- `chunked_ranges` is always stored sorted ascending by `start`.
- `gap_summary` is computed by the adapter on every write.
- The sidecar is written atomically after each range completes. When chunk-all-remaining
  processes multiple gaps and fails mid-way, the sidecar reflects only the gaps that
  completed — no partial-gap state.
- `chunk_ids` within each range are reserved for deterministic Azure Search document IDs.
  They become authoritative only after the chunk path can reliably return or persist them.
  Until then, exact index purge remains deferred.

---

## Blob Path Convention

The adapter synthesizes the blob path from upload metadata using the same rules as the
existing ingestion pipeline. The blob path mirrors the local `data/docs/` folder structure.

| source_type | module | year | Blob path |
|---|---|---|---|
| `banner` | `Finance` | `2026` | `banner/finance/releases/2026/<filename>` |
| `banner` | `General` | `2026` | `banner/general/releases/2026/<filename>` |
| `banner_user_guide` | `Student` | — | `banner/student/use/<filename>` |
| `banner_user_guide` | `Finance` | — | `banner/finance/use/<filename>` |

The sidecar blob path is always `{blob_path}.chunks.json`.

**Uniqueness assumption:** One PDF per blob path. If a file is re-uploaded at the same path,
the adapter returns a 409 Conflict. To replace a PDF, delete the existing blob and sidecar
first via `DELETE /banner/upload/{upload_id}`, then re-upload.

---

## Endpoints

### `POST /banner/upload`

Upload a PDF to Blob Storage. Creates the sidecar. Does not chunk.

**Request (multipart/form-data):**

| Field | Required | Description |
|---|---|---|
| `file` | Yes | The PDF file. |
| `source_type` | Yes | `banner` or `banner_user_guide`. |
| `module` | Yes | `General`, `Finance`, `Student`, etc. |
| `version` | No | e.g. `9.3.22` — release notes only. |
| `year` | No | e.g. `2026` — release notes only. |

**Response:**

```json
{
  "upload_id": "a3f8c1d2-...",
  "blob_path": "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
  "total_pages": 120,
  "status": "pending",
  "chunking_pattern": "none",
  "gap_count": 1,
  "gap_summary": "1 gap: pages 1-120 (120 pages total unchunked)",
  "message": "PDF uploaded. No pages chunked yet. Call POST /banner/upload/chunk to begin."
}
```

**Errors:**

| Code | Cause |
|---|---|
| 400 | Missing required field, unknown module, non-PDF extension. |
| 409 | A PDF already exists at this blob path. |
| 413 | File exceeds `MAX_UPLOAD_SIZE_MB` (default 100 MB). |

---

### `POST /banner/upload/from-url`

Download a PDF from a URL, write it to Blob Storage, and create the sidecar. Behaves
identically to `POST /banner/upload` after the download step — the same sidecar is created,
the same `upload_id` is returned, and all subsequent chunk, status, and resume calls work
unchanged. Does not chunk.

**Request (application/json):**

```json
{
  "url": "https://customercare.ellucian.com/downloads/Banner_Finance_9.3.22_Release_Notes.pdf",
  "source_type": "banner",
  "module": "Finance",
  "version": "9.3.22",
  "year": "2026"
}
```

| Field | Required | Description |
|---|---|---|
| `url` | Yes | HTTPS URL to the PDF. Must be on the configured allowlist. |
| `source_type` | Yes | `banner` or `banner_user_guide`. |
| `module` | Yes | `General`, `Finance`, `Student`, etc. |
| `version` | No | e.g. `9.3.22` — release notes only. |
| `year` | No | e.g. `2026` — release notes only. |

**Security constraints:**

- URL must be HTTPS — plain HTTP is rejected with 400.
- URL hostname must be on the server allowlist (`UPLOAD_URL_ALLOWLIST` env var, comma-separated).
  Default: `customercare.ellucian.com,ellucian.com`. Set to `*` to allow any HTTPS URL —
  not recommended in production.
- Download timeout: 60 seconds.
- File size limit: same as multipart upload (`MAX_UPLOAD_SIZE_MB`, default 100 MB).
- File extension validated after download — must be `.pdf`.

**Response:** identical shape to `POST /banner/upload`. `uploaded_at` reflects when the
download completed. If the download fails, no sidecar is created.

**Errors:**

| Code | Cause |
|---|---|
| 400 | HTTP (not HTTPS) URL provided. |
| 400 | URL hostname not on the allowlist. |
| 400 | Missing required field, unknown module, unsupported file extension after download. |
| 400 | `version` or `year` provided for `source_type=banner_user_guide`. |
| 404 | URL returned 404 from remote server. |
| 408 | Download timed out (60 second limit). |
| 409 | A PDF already exists at the synthesized blob path. |
| 413 | Downloaded file exceeds `MAX_UPLOAD_SIZE_MB`. |
| 502 | Remote server returned a 5xx error. |

---

### `POST /banner/upload/chunk`

Chunk a page range of an already-uploaded PDF. Reads from Blob, runs the ingest pipeline,
updates the sidecar.

**System-layer behavior:** the adapter accepts any non-overlapping page range regardless of
order. Pages 33-44 can be chunked before pages 1-32. The planned wizard layer (Agent 19)
will enforce left-to-right order by default.

**Request (application/json):**

```json
{
  "upload_id": "a3f8c1d2-...",
  "page_start": 1,
  "page_end": 50
}
```

Omit `page_start` and `page_end` to chunk all remaining unchunked pages. When multiple
unchunked gaps exist, the adapter iterates all gaps in ascending order, chunking each
sequentially and writing the sidecar after each gap completes.

```json
{
  "upload_id": "a3f8c1d2-..."
}
```

**Overlap detection:** for any new range `[page_start, page_end]`, the adapter checks every
existing entry in `chunked_ranges`. Rejected if for any existing `[s, e]`:
`page_start <= e AND page_end >= s`.

**Response:**

```json
{
  "upload_id": "a3f8c1d2-...",
  "pages_chunked": 50,
  "chunks_indexed": 38,
  "gaps_processed": 1,
  "gaps_remaining": 0,
  "chunked_ranges": [{"start": 1, "end": 50}],
  "unchunked_ranges": [{"start": 51, "end": 120}],
  "status": "partial",
  "chunking_pattern": "sequential",
  "gap_count": 1,
  "gap_summary": "1 gap: pages 51-120 (70 pages total unchunked)"
}
```

`gaps_processed` and `gaps_remaining` reflect how many unchunked intervals were processed
in this call. For a targeted range call, these are 1 and 0 respectively.

**Errors:**

| Code | Cause |
|---|---|
| 400 | Page range overlaps with an already-chunked range. |
| 400 | `page_end` exceeds `total_pages` or `page_start` < 1. |
| 404 | `upload_id` not found (PDF or sidecar missing from Blob). |
| 409 | A chunk run is already in progress for this `upload_id`. |

---

### `GET /banner/upload/{upload_id}/status`

Read the current sidecar state. Does not modify anything.

**Response:**

```json
{
  "upload_id": "a3f8c1d2-...",
  "blob_path": "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
  "total_pages": 120,
  "chunked_ranges": [
    {"start": 33, "end": 44, "chunked_at": "2026-04-26T10:05:00Z"},
    {"start": 78, "end": 90, "chunked_at": "2026-04-26T10:22:00Z"}
  ],
  "unchunked_ranges": [
    {"start": 1,  "end": 32},
    {"start": 45, "end": 77},
    {"start": 91, "end": 120}
  ],
  "status": "partial",
  "chunking_pattern": "sparse",
  "gap_count": 3,
  "gap_summary": "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)",
  "queryable_page_count": 25,
  "remaining_page_count": 95,
  "estimated_remaining_minutes": 7
}
```

`estimated_remaining_minutes` sums across all `unchunked_ranges`:
`ceil(sum(range.end - range.start + 1) * avg_chunks_per_page * 0.5s / 60)`.

---

### `GET /banner/upload`

List all tracked uploads. Returns sidecar summaries for all documents.

**Response:**

```json
[
  {
    "upload_id": "a3f8c1d2-...",
    "blob_path": "banner/finance/releases/2026/Banner_Finance_9.3.22.pdf",
    "status": "complete",
    "chunking_pattern": "sequential",
    "total_pages": 120,
    "queryable_page_count": 120,
    "gap_count": 0,
    "gap_summary": "fully indexed"
  },
  {
    "upload_id": "b9e2f1a0-...",
    "blob_path": "banner/student/use/Banner_Student_Use_Ellucian.pdf",
    "status": "partial",
    "chunking_pattern": "sparse",
    "total_pages": 480,
    "queryable_page_count": 25,
    "gap_count": 3,
    "gap_summary": "3 gaps: pages 1-32, 45-77, 91-480 (455 pages total unchunked)"
  }
]
```

---

### `DELETE /banner/upload/{upload_id}`

Remove the PDF blob and its sidecar. Does not remove chunks from the Azure Search index.
Exact index purge is deferred until uploaded page ranges reliably persist the chunk IDs needed
for tested search deletion.

**Response:**

```json
{
  "upload_id": "a3f8c1d2-...",
  "blob_deleted": true,
  "sidecar_deleted": true,
  "chunks_purged": false
}
```

---

## Full Workflow (Happy Path)

```bash
# 1. Upload
curl -s -X POST http://localhost:8000/banner/upload \
  -F "file=@Banner_Finance_9.3.22.pdf" \
  -F "source_type=banner" \
  -F "module=Finance" \
  -F "version=9.3.22" \
  -F "year=2026" | jq .
# → upload_id: "a3f8c1d2-...", total_pages: 120, status: "pending"

# 2. Chunk all pages
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"a3f8c1d2-..."}' | jq .
# → chunks_indexed: 87, status: "complete", chunking_pattern: "sequential"

# 3. Verify
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What changed in Banner Finance 9.3.22?","module_filter":"Finance","top_k":3}' | jq .
```

---

## URL Upload Workflow

```bash
# 1. Upload from URL (Ellucian ECC link)
curl -s -X POST http://localhost:8000/banner/upload/from-url \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://customercare.ellucian.com/downloads/Banner_Finance_9.3.22.pdf",
    "source_type": "banner",
    "module": "Finance",
    "version": "9.3.22",
    "year": "2026"
  }' | jq .
# → upload_id: "a3f8c1d2-...", total_pages: 120, status: "pending"

# 2. Chunk all pages — identical to the multipart path from here
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"a3f8c1d2-..."}' | jq .
# → chunks_indexed: 87, status: "complete"
```

**User guide from URL (no version or year):**

```bash
curl -s -X POST http://localhost:8000/banner/upload/from-url \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://customercare.ellucian.com/docs/Banner_Finance_Use_Ellucian.pdf",
    "source_type": "banner_user_guide",
    "module": "Finance"
  }' | jq .
```

**Allowlist error and fix:**

```bash
# Rejected — hostname not on allowlist
curl -s -X POST http://localhost:8000/banner/upload/from-url \
  -H "Content-Type: application/json" \
  -d '{"url":"https://internal.example.com/doc.pdf","source_type":"banner","module":"Finance","year":"2026"}' | jq .
# → 400: "URL hostname 'internal.example.com' is not on the allowed list."

# Fix: add hostname to allowlist (triggers automatic adapter restart — no deploy needed)
fly secrets set UPLOAD_URL_ALLOWLIST="customercare.ellucian.com,ellucian.com,internal.example.com"
```

---

## Partial Chunking Workflow

For large PDFs where you want to start querying quickly while the rest chunks later:

```bash
# 1. Upload
curl -s -X POST http://localhost:8000/banner/upload \
  -F "file=@Banner_Student_Use_Ellucian.pdf" \
  -F "source_type=banner_user_guide" \
  -F "module=Student" | jq .
# → upload_id: "b9e2f1a0-...", total_pages: 480, status: "pending"

# 2. Chunk the first 100 pages
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"b9e2f1a0-...","page_start":1,"page_end":100}' | jq .
# → chunks_indexed: 74, chunking_pattern: "sequential"
# → gap_summary: "1 gap: pages 101-480 (380 pages total unchunked)"

# 3. Continue later
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"b9e2f1a0-...","page_start":101,"page_end":250}' | jq .

# 4. Chunk all remaining (omit page range)
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"b9e2f1a0-..."}' | jq .
# → status: "complete", chunking_pattern: "sequential"
```

---

## Non-Contiguous Chunking

The system layer accepts any non-overlapping page range in any order. This produces a
`sparse` chunking pattern when the chunked ranges are not contiguous.

**When this occurs:**
- A developer calls the API directly with arbitrary ranges
- The planned Agent 19 advanced mode targets a specific chapter
- A chunk-all-remaining call fails mid-way, leaving some gaps filled and others not

**Example: chunking pages 33-44 then 78-90 on a 120-page document**

After the first call (`page_start=33, page_end=44`):

```json
{
  "chunked_ranges": [{"start": 33, "end": 44, "chunk_ids": [...]}],
  "unchunked_ranges": [{"start": 1, "end": 32}, {"start": 45, "end": 120}],
  "chunking_pattern": "contiguous",
  "gap_count": 2,
  "gap_summary": "2 gaps: pages 1-32, 45-120 (108 pages total unchunked)"
}
```

After the second call (`page_start=78, page_end=90`):

```json
{
  "chunked_ranges": [
    {"start": 33, "end": 44, "chunk_ids": [...]},
    {"start": 78, "end": 90, "chunk_ids": [...]}
  ],
  "unchunked_ranges": [
    {"start": 1,  "end": 32},
    {"start": 45, "end": 77},
    {"start": 91, "end": 120}
  ],
  "chunking_pattern": "sparse",
  "gap_count": 3,
  "gap_summary": "3 gaps: pages 1-32, 45-77, 91-120 (95 pages total unchunked)",
  "queryable_page_count": 25
}
```

**How `unchunked_ranges` is computed:**

1. Sort `chunked_ranges` ascending by `start`.
2. Merge any adjacent or overlapping intervals (defensive — overlap check prevents these, but
   merge is applied regardless).
3. Compute complement against `[1, total_pages]`: gap before first chunk, gaps between
   consecutive chunks, gap after last chunk. Omit any interval where `start > end`.

**How the overlap check works:**

A new range `[page_start, page_end]` is rejected if for any existing `[s, e]` in
`chunked_ranges`: `page_start <= e AND page_end >= s`. This catches full containment,
partial overlap from either side, and exact duplicates.

**Filling all gaps at once:**

```bash
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"a3f8c1d2-..."}' | jq .
# → gaps_processed: 3, gaps_remaining: 0, status: "complete", chunking_pattern: "sequential"
```

The adapter iterates all `unchunked_ranges` in ascending order and writes the sidecar after
each gap completes. If the call fails mid-way, the sidecar reflects completed gaps. Reissuing
the same no-range call resumes from the next incomplete gap.

**Querying a sparse document:**

Only chunks from indexed pages are findable. A query answered by page 20 returns nothing if
only pages 33-44 and 78-90 are chunked. `gap_summary` in the status response is the diagnostic
signal for this condition.

---

## Resuming an Incomplete Chunk Run

If a chunk call fails mid-way (network timeout, Azure rate limit, container restart), the sidecar
reflects only the pages successfully embedded before failure. No pages are double-indexed —
chunk IDs are deterministic in the current ingest pipeline.

```bash
# Check what completed
curl -s http://localhost:8000/banner/upload/{upload_id}/status \
  | jq '{status,chunking_pattern,gap_summary,unchunked_ranges}'

# Resume — omit page range to chunk all remaining gaps
curl -s -X POST http://localhost:8000/banner/upload/chunk \
  -H "Content-Type: application/json" \
  -d '{"upload_id":"{upload_id}"}' | jq .
```

---

## Checking Status

```bash
curl -s http://localhost:8000/banner/upload/{upload_id}/status | jq .
```

`status` values:

| Value | Meaning |
|---|---|
| `pending` | PDF is in Blob Storage. No pages chunked. Not queryable. |
| `partial` | Some pages chunked and queryable. `gap_summary` shows what remains. |
| `complete` | All pages chunked. Fully queryable. |

`chunking_pattern` values:

| Value | Meaning |
|---|---|
| `none` | No pages chunked yet. |
| `sequential` | One contiguous block starting at page 1. Normal left-to-right case. |
| `contiguous` | One contiguous block not starting at page 1. |
| `sparse` | Multiple non-contiguous chunked ranges. Queries may return incomplete results. |

List all uploads:

```bash
curl -s http://localhost:8000/banner/upload | jq .
```

---

## Queryability During Partial Ingest

Chunks are queryable immediately after each `POST /banner/upload/chunk` call completes.
Azure Search has no concept of a complete document — each chunk is independently indexed.

- A `partial` document with `chunking_pattern=sequential` has a predictable coverage boundary:
  "the first N pages are searchable."
- A `partial` document with `chunking_pattern=sparse` has unpredictable coverage: queries
  may silently miss relevant content from unchunked pages. Communicate `gap_summary` to any
  user who reports missing results.
- After `status=complete`, the document is identical in behavior to one ingested via
  `POST /banner/ingest`.

---

## Pre-Upload Checklist

- [ ] PDF is text-based (you can select text in a viewer) — not a scanned image
- [ ] `source_type` is correct: `banner` for release notes, `banner_user_guide` for how-to guides
- [ ] `module` is a recognized name: General, Finance, Student, HR, Financial Aid, Advancement, Payroll, Accounts Receivable, Position Control
- [ ] Release notes only: `version` (e.g. `9.3.22`) and `year` (e.g. `2026`) are provided
- [ ] User guides: do NOT provide `version` or `year`
- [ ] PDF-only for Phase U: SOP, DOCX, TXT, and Markdown upload are deferred
- [ ] File is under 100 MB
- [ ] No existing PDF at the same blob path (check `GET /banner/upload` list first)
- [ ] Azure Blob Storage env vars are set: `AZURE_STORAGE_CONNECTION_STRING`, `AZURE_STORAGE_CONTAINER_NAME`
- [ ] (from-url only) URL is HTTPS and the hostname is on `UPLOAD_URL_ALLOWLIST`
- [ ] (from-url only) URL is publicly reachable from the server — not behind a VPN or login wall

---

## Error Reference

| Error | Cause | Fix |
|---|---|---|
| 400 unknown module | `module` value not recognized | Use: General, Finance, Student, HR, Financial Aid, Advancement, Payroll, Accounts Receivable, Position Control |
| 400 version on user guide | `version` or `year` provided for `source_type=banner_user_guide` | Remove those fields |
| 400 range overlap | Requested range intersects any existing chunked range | Check `chunked_ranges` in status response, use a non-overlapping range |
| 400 range out of bounds | `page_end` > `total_pages` or `page_start` < 1 | Check `total_pages` in status response |
| 400 HTTP URL | `from-url` received a non-HTTPS URL | Use HTTPS |
| 400 URL not allowlisted | Hostname not in `UPLOAD_URL_ALLOWLIST` | Add hostname to env var |
| 404 upload not found | `upload_id` blob or sidecar missing | Upload the file again |
| 404 URL not found | `from-url` got 404 from remote server | Verify the URL is valid |
| 408 download timeout | Remote server took over 60 seconds | Retry, or download manually and use multipart |
| 409 already exists | PDF already at this blob path | Use existing `upload_id`, or DELETE first |
| 409 chunk in progress | Another chunk call running for this `upload_id` | Wait for it to complete |
| 413 file too large | Exceeds `MAX_UPLOAD_SIZE_MB` | Split the PDF or raise the limit |
| 502 remote server error | `from-url` received 5xx from remote | Retry later or use multipart upload |
