# Nice To Know

Practical context that prevents common wrong assumptions while working on citesearch.

---

## Search Scores Are Not Percentages

Azure AI Search hybrid scores are not normalized confidence percentages. Good RRF hybrid scores
often sit around `0.01` to `0.05`. A score of `0.033` with relevant sources can be a strong answer.

Do not set escalation logic as if scores range from `0` to `1`. See the score distribution section
in [RUNBOOK.md](RUNBOOK.md).

---

## Upload Is Not Ingest

`POST /banner/upload` and `POST /banner/upload/from-url` only store a PDF and create sidecar state.
They do not call Azure OpenAI, Azure AI Search, or `ingest.Run()`.

Pages become queryable only after:

```text
POST /banner/upload/chunk
```

This separation is intentional so large PDFs can be chunked in stages.

---

## Sidecar State Is The Upload Source Of Truth

Phase U stores upload state in a JSON sidecar beside the PDF:

```text
{blob_path}.chunks.json
```

The sidecar tracks:

- `upload_id`
- `total_pages`
- `chunked_ranges`
- `unchunked_ranges`
- `status`
- `chunking_pattern`
- `gap_summary`

If a sidecar is deleted, the API no longer knows that upload exists, even if the PDF blob still
exists.

---

## Delete Does Not Purge Search Yet

`DELETE /banner/upload/{upload_id}` removes the PDF blob and sidecar. It does not purge chunks
from Azure AI Search.

`?purge_index=true` currently returns `501 Not Implemented` and leaves storage untouched. Exact
search purge is deferred until chunk IDs are persisted and tested end to end.

---

## Blob Sync And Phase U Upload Are Different Paths

`POST /banner/blob/sync` ingests blobs already present in the container.

Phase U upload creates tracked upload state and supports staged chunking. Use status/list/delete
only for Phase U uploads because those endpoints read sidecar files.

---

## The Adapter Should Stay Thin

The Botpress adapter should:

- Classify intent.
- Route to the backend.
- Normalize response fields for Botpress.

It should not:

- Embed text.
- Search Azure AI Search.
- Build RAG prompts.
- Read local documents.
- Perform ingestion.

---

## Dry Runs Should Not Hit Azure

Dry-run ingest behavior is for preflight validation. It should walk files, infer metadata, and
report warnings without embedding text or uploading Search documents.

---

## Chunk Size Affects Cost And Quality

Smaller chunks can improve pinpoint retrieval but increase embedding count and Search documents.
Larger chunks reduce document count but can dilute relevance.

Defaults live in `.env.example`:

```env
CHUNK_SIZE=1000
CHUNK_OVERLAP=150
TOP_K_DEFAULT=5
```

Change these deliberately and re-ingest when evaluating answer quality.

---

## Free-Tier ngrok URLs Change

If using a free ngrok tunnel, the public URL changes when ngrok restarts. Update any dependent
Fly.io secret or Botpress environment variable after restarting ngrok.

Useful checks:

```bash
curl -s http://localhost:4040/api/tunnels | jq -r '.tunnels[0].public_url'
fly secrets list
```

---

## Use Admin Search Keys For Ingest

Azure AI Search query keys are read-only. Creating indexes and uploading documents require an
admin key.

If queries work but ingest or index creation fails with `403`, check `AZURE_SEARCH_API_KEY`.

---

## Docs Are Part Of The Contract

Update docs when changing:

- Public endpoints.
- Request or response fields.
- Env vars.
- Ingest source rules.
- Upload/chunk sidecar behavior.
- Operator workflows.
- Failure modes.

The most frequently updated docs are README, [HOW-TO.md](HOW-TO.md), [TESTING.md](TESTING.md),
[TROUBLESHOOTING.md](TROUBLESHOOTING.md), and [../ingest/PDF_UPLOAD_FLOW.md](../ingest/PDF_UPLOAD_FLOW.md).

