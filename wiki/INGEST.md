# Ingest Pipeline — Operator Reference

Everything you need to know before, during, and after an ingest run.
For the internal Go implementation, see [INTERNALS.md](INTERNALS.md) § Data Flow: Ingestion Pipeline.

---

## Table of Contents

1. [Canonical Folder Structure](#canonical-folder-structure)
2. [File Naming Conventions](#file-naming-conventions)
3. [How Metadata Is Extracted](#how-metadata-is-extracted)
4. [Supported File Types](#supported-file-types)
5. [Chunking Strategy Selection](#chunking-strategy-selection)
6. [Source Type Tagging](#source-type-tagging)
7. [Pre-Ingest Checklist](#pre-ingest-checklist)
8. [Running an Ingest](#running-an-ingest)
9. [Re-Ingest Safety](#re-ingest-safety)
10. [Finance User Guide Gap](#finance-user-guide-gap)
11. [Ingestion Time Estimates](#ingestion-time-estimates)
12. [Troubleshooting Bad PDFs](#troubleshooting-bad-pdfs)
13. [How to Add a New Document Collection](#how-to-add-a-new-document-collection)
14. [Upload Workflow — Ingest Without Filesystem Access](#upload-workflow--ingest-without-filesystem-access)

---

## Canonical Folder Structure

**The folder path IS the metadata.** Module, year, and source type are all derived from the path — not from the PDF content or internal PDF metadata.

```
data/
└── docs/
    ├── banner/
    │   ├── general/
    │   │   ├── releases/
    │   │   │   └── 2026/
    │   │   │       └── february/
    │   │   │           └── Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf
    │   │   └── use/
    │   │       └── Banner General - Use - Ellucian.pdf
    │   ├── student/
    │   │   ├── releases/            ← (not currently used; student releases go in /march etc.)
    │   │   └── use/
    │   │       └── Banner Student - Use - Ellucian.pdf
    │   └── finance/
    │       ├── releases/            ← finance release notes (not yet ingested)
    │       └── use/                 ← finance user guide PDFs (not yet acquired)
    └── sop/
        ├── SOP122 - Smoke Test and Sanity Test Post Banner Upgrade.docx
        └── SOP154 - Procedure - Start, Stop Axiom.docx
```

### Rule: folder name determines module

| Folder name contains | Mapped module |
|---|---|
| `general` | `General` |
| `finance` | `Finance` |
| `student` | `Student` |
| `hr` or `human_resources` | `Human Resources` |
| `financial_aid` | `Financial Aid` |
| `advancement` | `Advancement` |
| `payroll` | `Payroll` |
| `accounts_receivable` | `Accounts Receivable` |
| `position_control` | `Position Control` |
| (none match) | `""` (empty — no module tag) |

**Implication:** A PDF placed in `data/docs/banner/finance/` will be tagged `banner_module=Finance`. If you put it in `data/docs/banner/` it will have no module tag and queries with `module_filter=Finance` will never find it.

### Rule: `/use/` path triggers user-guide source type

Any file whose path contains `/use/` (or `\use\`) receives `source_type=banner_user_guide`.
Everything else under `banner/` receives `source_type=banner`.

### Rule: `/sop/` path triggers SOP processing

Any file under a path containing `/sop/` is processed by the SOP-specific pipeline (DOCX only).

---

## File Naming Conventions

### Banner release note PDFs

The version is extracted from the **filename** by regex `(\d+\.\d+\.\d+(?:\.\d+)?)`.

| Filename | Extracted version |
|---|---|
| `Banner_General_Release_Notes_9.3.37.2_8.26.2_February_2026.pdf` | `9.3.37.2` |
| `Banner Finance 9.3.22.pdf` | `9.3.22` |
| `Banner General - Use - Ellucian.pdf` | *(none — correct, user guides have no version)* |

**The year is extracted from the folder path**, not the filename.
Example: `.../releases/2026/...` → `year=2026`.

**Good naming practice:**
```
Banner_<Module>_Release_Notes_<version>_<date>.pdf
```
e.g. `Banner_Finance_Release_Notes_9.3.22_March_2026.pdf`

### User guide PDFs

User guides carry no version or year. Naming is free-form, but keep it descriptive:
```
Banner <Module> - Use - Ellucian.pdf
```
No version in the path — never put user guide PDFs under a dated folder.

### SOP DOCX files

Strict convention enforced by the parser — files that don't match are silently skipped:
```
SOP<number> - <title>.docx
```
e.g. `SOP154 - Procedure - Start, Stop Axiom.docx`

The number must be all digits. The title may contain any characters except a leading dot.

---

## How Metadata Is Extracted

All metadata comes from the **file path**, not PDF properties.

| Metadata field | Source | Rule |
|---|---|---|
| `banner_module` | Folder name | First matching module keyword in path (case-insensitive) |
| `banner_version` | Filename | First version-like regex match (`\d+\.\d+\.\d+`) that doesn't start with `20` (year guard) |
| `year` | Folder path | First 4-digit number in path matching `\b20\d{2}\b` |
| `source_type` | Path segment | `/use/` → `banner_user_guide`; `/sop/` → `sop`; otherwise → `banner` |
| `sop_number` | Filename | SOP DOCX only — regex `SOP(\d+)` |
| `document_title` | Filename | SOP DOCX only — text after ` - ` in filename |

**Gotcha — version regex matches year:** The version regex `\d+\.\d+\.\d+` will also match a date like `8.26.2` in the filename. The code filters out matches starting with `20` (year guard), so `Banner_General_9.3.37.2_8.26.2.pdf` correctly picks `9.3.37.2` over `8.26.2`.

**Gotcha — module must be a known string:** Only the module names in the lookup list are recognized. If you create a folder named `Banner_AR` instead of `Accounts_Receivable`, the module will be blank.

---

## Supported File Types

| Extension | Processing path | Notes |
|---|---|---|
| `.pdf` | `extractPDFPages()` via `ledongthuc/pdf` | Text-layer PDFs only. Scanned/image PDFs extract garbage. |
| `.txt` / `.md` | `extractTextFile()` | Treated as a single page. No page number metadata. |
| `.docx` | SOP path only (`isSopDocument()`) | Standard OOXML ZIP format. Tables are skipped. |
| `.docx` outside `/sop/` | *Not processed* | The SOP check is path-based; DOCX outside `/sop/` is unsupported. |

---

## Chunking Strategy Selection

The ingest code selects a chunking strategy per file, based on `source_type`.

| source_type | Strategy | Where to look |
|---|---|---|
| `banner_user_guide` | `chunkStudentText()` — section-aware, heading-triggered boundaries, breadcrumb prefix per chunk | `internal/ingest/student_chunker.go` |
| `banner` | `chunkText()` — character-based with overlap, tries to break on paragraph/sentence boundaries | `internal/ingest/ingest.go` |
| `sop` | `chunkSop()` — section-aware, heading-triggered, breadcrumb prefix, covers DOCX only | `internal/ingest/sop_chunker.go` |

### Why section-aware chunking matters for user guides

User guide PDFs have numbered sections (e.g., `3.1 Course Search`) and all-caps headings (e.g., `COURSE CATALOG`). The section-aware chunker detects these heading lines and:
1. Starts a new chunk when a heading is seen
2. Prefixes every chunk with `[<heading>] ` — the "breadcrumb"

This ensures the vector embedding carries section context. Without it, a chunk like "Click the magnifying glass icon" has no context that it's about `COURSE CATALOG`.

### Limitation: chunking is calibrated for Student user guide

`chunkStudentText` was tuned for the Ellucian Banner Student user guide document structure. Finance and General user guides may have different heading conventions. If Finance user guide PDFs are added and retrieval quality is poor, revisit the heading detection regexes in `student_chunker.go`.

**Current heading detection regexes:**
- Numbered: `^\d+(\.\d+)*\s+[A-Z]` — matches `3.1 Course Search`
- All-caps: `^[A-Z][A-Z\s\-/,()&]{7,}$` — matches `COURSE CATALOG` but not `Course Catalog`

---

## Source Type Tagging

| Path condition | source_type | Backend endpoint that serves it |
|---|---|---|
| Contains `/sop/` | `sop` | `POST /sop/ask` |
| Contains `/use/` | `banner_user_guide` | `POST /banner/:module/ask` with `source_type=banner_user_guide` |
| All other banner PDFs | `banner` | `POST /banner/ask` with `module_filter` |

**Never set `version_filter` or `year_filter` for user guide queries.** User guide PDFs have no version metadata (the fields are empty in the index). Filtering by version returns 0 results.

---

## Pre-Ingest Checklist

Run through this before triggering any ingest:

- [ ] **Folder placement** — PDF is in the correct module folder (`general/`, `finance/`, `student/`)
- [ ] **Source type path** — Release notes: under `releases/YYYY/`; User guides: under `use/`; SOPs: under `sop/`
- [ ] **Filename version** — Release note PDFs have version in the name (`9.3.37.2`); user guide PDFs do not
- [ ] **Year folder** — Release note PDFs are in a year folder (`2026/`) so `year=2026` is extracted
- [ ] **SOP naming** — DOCX files follow `SOP<n> - <title>.docx` exactly
- [ ] **PDF is text-based** — Open the PDF and try selecting text. If you can't select text, it's a scanned image PDF and will produce garbage (see [Troubleshooting Bad PDFs](#troubleshooting-bad-pdfs))
- [ ] **Index exists** — `curl http://localhost:8000/index/stats` returns a valid response, not a 404
- [ ] **Environment variables** — `.env` has valid Azure credentials (OpenAI endpoint + key, Search endpoint + key)
- [ ] **`overwrite` flag** — Use `false` unless you intend to delete and rebuild the entire index (see [Re-Ingest Safety](#re-ingest-safety))

---

## Running an Ingest

### Ingest banner release notes

```bash
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "docs_path": "data/docs/banner/general/releases",
    "overwrite": false
  }' | jq .
```

Expected response:
```json
{
  "status": "success",
  "documents_processed": 1,
  "chunks_indexed": 142,
  "message": "Ingested 1 documents (142 chunks) into \"banner-index\""
}
```

### Ingest user guide PDFs

```bash
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "docs_path": "data/docs/banner/general/use",
    "overwrite": false
  }' | jq .
```

### Ingest SOP documents

```bash
curl -s -X POST http://localhost:sop/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "docs_path": "data/docs/sop",
    "overwrite": false
  }' | jq .
```

### Ingest a specific page range (partial re-ingest)

Useful if a large PDF partially failed. Requires knowing the page numbers from the logs.

```bash
curl -s -X POST http://localhost:8000/banner/ingest \
  -H "Content-Type: application/json" \
  -d '{
    "docs_path": "data/docs/banner/general/releases",
    "overwrite": false,
    "start_page": 50,
    "end_page": 100
  }' | jq .
```

### Verify what was indexed

```bash
curl -s http://localhost:8000/index/stats | jq .
```

Test a query immediately after ingest:

```bash
curl -s -X POST http://localhost:8000/banner/ask \
  -H "Content-Type: application/json" \
  -d '{"question":"What changed in Banner General?","module_filter":"General","top_k":3}' \
  | jq '{count:.retrieval_count, score:.sources[0].score, answer:.answer[:100]}'
```

---

## Re-Ingest Safety

### Scenario: re-ingest after updating a PDF

Safe — chunk IDs are deterministic (`MD5(filename + page + index)`). Re-ingesting the same file
with the same `CHUNK_SIZE` updates chunks in place. Azure Search's merge-or-upload semantics
handle this correctly.

### Scenario: re-ingest after changing `CHUNK_SIZE` or `CHUNK_OVERLAP`

**Dangerous if you use `overwrite=false`.** Changing chunk parameters produces new chunk IDs.
Old chunks (with the previous ID) remain in the index alongside the new ones. The same document
is now double-indexed with conflicting chunks.

**Correct procedure:**
1. Set `overwrite=true` — this deletes the **entire** index
2. Re-ingest **all documents** (Banner PDFs, user guides, AND SOPs)
3. Do not skip SOPs — `overwrite=true` removes them too

### Scenario: ingest fails midway through a large PDF

Partial ingest — chunks from completed pages are in the index; remaining pages are not.
To complete the ingest without duplicating already-indexed chunks, use page range targeting:

```bash
# Check logs to find last successfully processed page, then resume from there
curl ... -d '{"docs_path":"...", "overwrite": false, "start_page": <resume_from>}'
```

### `overwrite: true` — what it actually deletes

`overwrite=true` calls `search.CreateIndex()` which **drops and recreates the entire Azure AI
Search index**. This includes:
- All Banner release note chunks
- All Banner user guide chunks
- All SOP chunks

After `overwrite=true`, you must re-ingest every document collection from scratch.

---

## Finance User Guide Gap

### Current state (as of 2026-04-23)

| Collection | Folder | PDFs present | API status |
|---|---|---|---|
| General release notes | `data/docs/banner/general/releases/` | ✅ Yes | ✅ Works |
| General user guide | `data/docs/banner/general/use/` | ✅ Yes | ✅ Works |
| Student user guide | `data/docs/banner/student/use/` | ✅ Yes | ✅ Works |
| Finance release notes | `data/docs/banner/finance/releases/` | ❌ Not ingested | Returns 0 results |
| Finance user guide | `data/docs/banner/finance/use/` | ❌ Not acquired | Returns 400 |
| SOP documents | `data/docs/sop/` | ✅ Partial (2 SOPs) | ✅ Works |

### Why `source=user_guide_finance` returns 400

The adapter returns 400 by design when the Finance user guide PDFs are not yet indexed — prevents
routing questions to an empty source. The 400 will automatically resolve once Finance user guide
PDFs are ingested.

### Acquiring Finance user guide PDFs

Ellucian provides Banner user guides via the Ellucian Customer Center (ECC). Steps:

1. Log into ECC at `ellucian.com/customer-center`
2. Navigate to **Documentation > Banner Finance**
3. Download the current **User Reference Manual** (PDF)
4. Place in `data/docs/banner/finance/use/Banner Finance - Use - Ellucian.pdf`
5. Run: `curl -X POST http://localhost:8000/banner/ingest -d '{"docs_path":"data/docs/banner/finance/use","overwrite":false}'`
6. Verify: `curl -X POST http://localhost:8000/banner/finance/ask -d '{"question":"How do I enter a journal entry?","source_type":"banner_user_guide"}'`

### Finance release notes

Finance release note PDFs follow the same naming and folder convention as General:
```
data/docs/banner/finance/releases/2026/march/Banner_Finance_Release_Notes_9.3.37.2_March_2026.pdf
```
Ingest with `module_filter=Finance` will work automatically once PDFs are in place.

---

## Ingestion Time Estimates

Time is dominated by Azure OpenAI embedding calls (one call per chunk, 500ms sleep between calls).

| Document type | Typical pages | Typical chunks | Approx. time |
|---|---|---|---|
| Banner release note (single version) | 10–30 | 20–80 | 2–7 min |
| Banner user guide (General) | 200–600 | 400–1200 | 30–90 min |
| Banner user guide (Student) | 300–800 | 600–1600 | 50–120 min |
| SOP document | — (section-based) | 5–30 per SOP | 1–4 min per SOP |

### How to estimate before starting

1. Open the PDF and note the page count
2. Estimate chunks: `ceil(pages * CHUNK_SIZE / avg_page_chars)` — rough rule is 2–4 chunks/page for dense PDFs
3. Time = chunks × 0.5 seconds

With default `CHUNK_SIZE=500`, a 400-page user guide produces ~800–1200 chunks → **7–10 minutes**.

### Reducing ingest time

- Lower `CHUNK_SIZE` in `.env` → fewer, larger chunks → fewer embedding calls (lower quality trade-off)
- Remove or reduce the 500ms sleep in `internal/ingest/ingest.go` if your Azure OpenAI TPM allows it
- Use `start_page` / `end_page` to ingest in parallel across multiple terminal sessions (non-overlapping page ranges)

---

## Troubleshooting Bad PDFs

### Symptom: chunks contain garbage characters or empty text

**Cause:** The PDF is scanned (image-based). The `ledongthuc/pdf` library only reads the text layer;
it has no OCR capability.

**How to identify before ingesting:**
1. Open the PDF in a viewer and try to select text
2. If you can't select text, it's a scanned PDF
3. File size check: scanned PDFs are typically >200KB/page; text PDFs are 20–80KB/page

**Fix options:**
- **Azure AI Document Intelligence** — use the Layout or Read API to extract text with OCR, save as `.txt`, then ingest the `.txt` file
- **Adobe Acrobat** — run "Recognize Text" (OCR) to create a searchable PDF before ingesting
- **If the PDF is from Ellucian:** download the PDF again — Ellucian PDFs are always text-based; a scanned version usually means the wrong file was downloaded

### Symptom: `banner_module` is empty in retrieved chunks

**Cause:** The PDF was placed in a folder whose name doesn't match any known module.

**Fix:** Move the PDF to a correctly named folder (`general/`, `finance/`, `student/`, etc.) and re-ingest.

### Symptom: `banner_version` is empty in retrieved chunks

**Cause:** The filename doesn't contain a version string matching `\d+\.\d+\.\d+`.

**Fix:** Rename the file to include the version (e.g., `Banner_General_9.3.37.2_ReleaseNotes.pdf`) and re-ingest.

**Note:** User guide PDFs correctly have no version. If you see `banner_version=""` for a user guide chunk, that is expected and correct.

### Symptom: SOP file is silently skipped during ingest

**Cause:** The filename doesn't match `SOP\d+ - .+\.docx`.

Common mistakes:
- Using an underscore instead of a space-dash-space: `SOP154_Procedure.docx` → **fails**
- Missing the SOP number: `Smoke Test Procedure.docx` → **fails**
- Wrong extension: `SOP154 - Title.pdf` → **fails** (SOPs must be DOCX)

**Fix:** Rename to `SOP<n> - <title>.docx` exactly.

### Symptom: version_filter query returns 0 results for a user guide query

**Cause:** User guide PDFs have no version metadata. Passing `version_filter` with a user guide
source type will always return 0 results.

**Fix:** Remove `version_filter` from user guide queries. See [Source Type Tagging](#source-type-tagging).

### Symptom: `POST /banner/finance/ask` with `source_type=banner_user_guide` returns 0 results

**Cause:** Finance user guide PDFs have not been ingested yet.

**Fix:** Acquire the Finance user guide PDF and ingest it (see [Finance User Guide Gap](#finance-user-guide-gap)).

---

## How to Add a New Document Collection

Use this checklist when adding a new Banner module's documents:

1. **Create the folder structure:**
   ```
   data/docs/banner/<module>/
   ├── releases/
   │   └── <year>/
   └── use/
   ```
   Use a module name that matches one of the known module strings (see [Canonical Folder Structure](#canonical-folder-structure)).

2. **Name release note PDFs** with the version in the filename:
   ```
   Banner_<Module>_Release_Notes_<version>_<date>.pdf
   ```

3. **Name user guide PDFs** without a version:
   ```
   Banner <Module> - Use - Ellucian.pdf
   ```

4. **Verify metadata extraction** before a full ingest by checking a small batch:
   ```bash
   curl -X POST http://localhost:8000/banner/ingest \
     -d '{"docs_path":"data/docs/banner/<module>/releases","overwrite":false,"end_page":5}'
   curl http://localhost:8000/index/stats
   ```

5. **Wire the new module in the adapter** if it needs a source override value
   (see `CLAUDE.md` § Source override table and `api/handlers.go`).

6. **Test retrieval** after full ingest:
   ```bash
   curl -X POST http://localhost:8000/banner/ask \
     -d '{"question":"What changed in Banner <Module>?","module_filter":"<Module>","top_k":3}'
   ```

---

## Upload Workflow — Ingest Without Filesystem Access

The folder-based ingest (`POST /banner/ingest`) requires the operator to place files in the
server's local `data/docs/` directory. In cloud deployments (Fly.io, remote Docker) this is
impractical — there is no SSH access and no persistent local filesystem.

Phase U introduces upload paths that do not require filesystem access:

| Path | Endpoint | Best for |
|---|---|---|
| Folder-based (existing) | `POST /banner/ingest` | Local dev, bulk initial ingest from disk |
| Azure Blob sync (existing) | `POST /banner/blob/sync` | Production, pre-populated Blob container |
| Upload — multipart (Phase U.5) | `POST /banner/upload` | Ad-hoc file upload to Blob. Creates sidecar. Does not chunk. |
| Upload — from URL (Phase U.6) | `POST /banner/upload/from-url` | Ellucian ECC download links, automation. Creates sidecar. Does not chunk. |
| Chunk (Phase U.7) | `POST /banner/upload/chunk` | Chunk a page range of an uploaded PDF. |
| Status/list (Phase U.8/U.9) | `GET /banner/upload...` | Manage sidecar-backed upload state. |
| Delete (Phase U.10) | `DELETE /banner/upload/{id}` | Remove blob and sidecar. Exact index purge is deferred until chunk IDs are persisted reliably. |

---

### How the upload path works

The upload flow is decoupled from chunking. An upload handler validates metadata, synthesizes
the Blob path, writes the PDF to Azure Blob Storage, counts pages with `ingest.CountPages()`,
and creates `{blob_path}.chunks.json`. It does not call Azure OpenAI, Azure AI Search, or
`ingest.Run()` until a later chunk request.

**Blob path synthesis rules (upload metadata → blob path):**

| source_type | module | year | Blob path |
|---|---|---|---|
| `banner` | `General` | `2026` | `banner/general/releases/2026/<filename>` |
| `banner` | `Finance` | `2026` | `banner/finance/releases/2026/<filename>` |
| `banner_user_guide` | `General` | — | `banner/general/use/<filename>` |
| `banner_user_guide` | `Student` | — | `banner/student/use/<filename>` |
| `banner_user_guide` | `Finance` | — | `banner/finance/use/<filename>` |

The sidecar blob path is always `{blob_path}.chunks.json`.

---

### `POST /banner/upload` — multipart form upload

Accepts a single PDF with explicit metadata. The server synthesizes the Blob path, writes the
PDF, counts pages, and creates the sidecar. It does not chunk.

**Request (multipart/form-data):**

| Field | Type | Required | Description |
|---|---|---|---|
| `file` | binary | Yes | PDF file |
| `source_type` | string | Yes | `banner` \| `banner_user_guide` |
| `module` | string | Yes | `General`, `Finance`, `Student`, etc. |
| `version` | string | Release notes | e.g. `9.3.37.2` — omit for user guides |
| `year` | string | Release notes | e.g. `2026` — omit for user guides |

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

**Validation rules:**
- `source_type` must be one of: `banner`, `banner_user_guide`
- `source_type=sop` is rejected in Phase U
- `module` is required
- `module` must match a known module name (case-insensitive)
- File size limit: 100 MB (configurable via `MAX_UPLOAD_SIZE_MB` env var)
- Accepted extension: `.pdf`
- DOCX, TXT, and Markdown upload are rejected before Blob write or sidecar creation

**Example curl:**
```bash
curl -s -X POST http://localhost:8000/banner/upload \
  -F "file=@Banner_Finance_9.3.22_Release_Notes.pdf" \
  -F "source_type=banner" \
  -F "module=Finance" \
  -F "version=9.3.22" \
  -F "year=2026" | jq .
```

**Upload a Banner user guide:**
```bash
curl -s -X POST http://localhost:8000/banner/upload \
  -F "file=@Banner Finance - Use - Ellucian.pdf" \
  -F "source_type=banner_user_guide" \
  -F "module=Finance" | jq .
```

### `POST /banner/upload/from-url` — URL-based upload

Provide a URL and metadata. The server downloads the PDF, writes it to Blob Storage, and creates
the sidecar. Useful for Ellucian Customer Center (ECC) download links in automated pipelines.

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

**Response:** same shape as `POST /banner/upload`.

**Security constraints enforced by the server:**
- URL must be HTTPS — HTTP rejected with 400
- URL hostname must be on the configured allowlist (`UPLOAD_URL_ALLOWLIST` env var, comma-separated)
  - Default allowlist: `customercare.ellucian.com,ellucian.com`
  - Set to `*` to allow any HTTPS URL (not recommended in production)
- Download timeout: 60 seconds
- File size limit: 100 MB (same as multipart upload)
- File extension validated after download (must be `.pdf`)
- `source_type=sop`, DOCX, TXT, and Markdown uploads are rejected before Blob or sidecar writes

**Errors:**

| Code | Cause |
|---|---|
| 400 | HTTP URL, disallowed hostname, missing metadata, unknown module, non-PDF download, unsupported source type, or `banner_user_guide` with `version`/`year`. |
| 404 | Remote URL returned 404. |
| 408 | Download timed out. |
| 409 | A PDF already exists at the synthesized blob path. |
| 413 | Downloaded file exceeds `MAX_UPLOAD_SIZE_MB`. |
| 502 | Remote server returned a 5xx error. |

**Example curl:**
```bash
curl -s -X POST http://localhost:8000/banner/upload/from-url \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://customercare.ellucian.com/.../Banner_Finance_9.3.22.pdf",
    "source_type": "banner",
    "module": "Finance",
    "version": "9.3.22",
    "year": "2026"
  }' | jq .
```

---

### `POST /banner/upload/chunk` — chunk uploaded pages

Chunk a page range of an already-uploaded PDF. The server resolves `upload_id` to its
sidecar, downloads the PDF from Blob to a temporary local path, runs the ingest pipeline only
for the requested pages, appends the completed range to `chunked_ranges`, recomputes derived
sidecar fields, and writes the sidecar after each completed gap.

**Request (application/json):**
```json
{
  "upload_id": "a3f8c1d2-...",
  "page_start": 1,
  "page_end": 50
}
```

Omit `page_start` and `page_end` to chunk all remaining unchunked ranges in ascending order:
```json
{
  "upload_id": "a3f8c1d2-..."
}
```

**Range rules:**
- `upload_id` is required
- `page_start` and `page_end` must be provided together
- `page_start` must be at least 1
- `page_end` must be greater than or equal to `page_start`
- `page_end` must not exceed `total_pages`
- Requested ranges must not overlap an existing `chunked_ranges` entry

Overlap detection rejects a new range `[page_start, page_end]` when any existing
`[s, e]` satisfies `page_start <= e AND page_end >= s`.

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

`gaps_processed` and `gaps_remaining` are scoped to the current call. `gap_count` and
`unchunked_ranges` describe the persisted sidecar state after the call.

The sidecar can persist `chunk_ids` when an ingest runner returns them. The current production
`ingest.Run()` summary reports chunk counts but not chunk IDs, so exact index purge remains
deferred until the ingest result exposes those IDs.

**Errors:**

| Code | Cause |
|---|---|
| 400 | Missing `upload_id`, incomplete page range, overlap, or out-of-bounds page range. |
| 404 | `upload_id` not found, PDF missing, or sidecar missing from Blob. |
| 409 | A chunk run is already active for this `upload_id` in the same API process. |
| 500 | Blob download, ingest, or sidecar write failed. |

### `DELETE /banner/upload/{upload_id}`

Delete the uploaded PDF blob and its `{blob_path}.chunks.json` sidecar. This endpoint does
not purge Azure Search chunks in Phase U.10.

**Response:**
```json
{
  "upload_id": "a3f8c1d2-...",
  "blob_deleted": true,
  "sidecar_deleted": true,
  "chunks_purged": false
}
```

`?purge_index=true` returns `501 Not Implemented` and leaves the blob and sidecar in place
until uploaded chunk IDs are reliably persisted and a tested search purge path exists.

**Errors:**

| Code | Cause |
|---|---|
| 404 | `upload_id` sidecar not found. |
| 501 | `purge_index=true` requested before index purge support exists. |
| 500 | Blob or sidecar deletion failed. |

If chunking all remaining gaps fails midway, completed gaps stay recorded because the sidecar
is written after each successful gap. Repeating the no-range request resumes from the remaining
`unchunked_ranges`.

---

### Azure Blob Storage as a durable PDF store

The existing `POST /banner/blob/sync` downloads from Blob and ingests. Phase U upload stores
PDFs in Blob as the canonical, durable store and stores chunking state in the adjacent sidecar.

**Blob path mirrors the local path:**
```
Blob container:  banner/finance/releases/2026/Banner_Finance_9.3.22.pdf
Sidecar:         banner/finance/releases/2026/Banner_Finance_9.3.22.pdf.chunks.json
```

With this layout, upload, chunk, status, list, and delete can all resolve state from Blob
without relying on local filesystem persistence.

**Why this matters for cloud deployments:**
- Container restarts lose the local `data/docs/` state
- With Blob as source of truth, `POST /banner/blob/sync` on startup rebuilds the index from Blob
- The sidecar persists across restarts — partial chunking state is never lost

**Env vars required for Blob storage:**
```env
AZURE_STORAGE_CONNECTION_STRING=DefaultEndpointsProtocol=https;AccountName=...
AZURE_STORAGE_CONTAINER_NAME=banner-docs
AZURE_STORAGE_BLOB_PREFIX=banner/          # optional prefix within the container
```

---

### Upload workflow pre-ingest checklist

Before uploading a PDF:

- [ ] **PDF is text-based** (can select text in viewer) — not scanned
- [ ] **source_type is correct**: `banner` for release notes, `banner_user_guide` for how-to guides
- [ ] **module is a recognized name** for banner/user_guide: General, Finance, Student, HR, etc.
- [ ] **For release notes**: provide `version` (e.g. `9.3.22`) and `year` (e.g. `2026`)
- [ ] **For user guides**: do NOT provide `version` or `year` — user guides are not versioned
- [ ] **PDF-only for Phase U**: SOP, DOCX, TXT, and Markdown upload are deferred
- [ ] **File size**: PDF is under 100 MB (user guides rarely exceed 50 MB)
- [ ] **Blob storage env**: `AZURE_STORAGE_CONNECTION_STRING` and `AZURE_STORAGE_CONTAINER_NAME` are configured

---

### Existing Blob sync endpoints (for reference)

These endpoints already exist and are separate from Phase U upload:

**List what's in Blob Storage:**
```bash
curl http://localhost:8000/banner/blob/list?prefix=banner/finance
```

**Sync all Blob documents to local and ingest:**
```bash
curl -s -X POST http://localhost:8000/banner/blob/sync \
  -H "Content-Type: application/json" \
  -d '{
    "prefix": "banner/finance",
    "overwrite": false
  }' | jq .
```

`ingest_after_sync` is always set to `true` by the handler — every blob sync triggers an ingest
of the downloaded folder.

**Limitation of current blob sync:** It always ingests the entire `data/docs/banner` folder
after download. Phase U upload/chunk tracks one uploaded PDF and its page ranges through the
sidecar.
