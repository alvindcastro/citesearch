# Ingest Docs Index

This folder contains planning and operator docs for ingest and upload-based chunking.

Recommended reading order:

1. [INGEST_FLOW.md](INGEST_FLOW.md) — end-to-end narrative from entry point selection through
   queryable chunks.
2. [INGEST.md](INGEST.md) — operator reference for filesystem ingest, upload workflow, naming
   rules, preflight checks, and troubleshooting.
3. [PDF_UPLOAD_FLOW.md](PDF_UPLOAD_FLOW.md) — upload/chunk endpoint contracts, response shapes,
   sidecar schema, partial chunking examples, and error reference.
4. [UPLOAD_SPEC_DECISION.md](UPLOAD_SPEC_DECISION.md) — Phase U.0 scope lock for the first
   upload-based ingest implementation.
5. [INTERNALS.md](INTERNALS.md) — implementation notes for the Go backend and ingest pipeline.
6. [TDD_PROMPTS.md](TDD_PROMPTS.md) — strict TDD phase prompts for implementing upload-based
   ingest.

Housekeeping notes:

- `INGEST_FLOW.md` is the canonical flow narrative.
- `PDF_UPLOAD_FLOW.md` is the canonical endpoint contract for the upload/chunk path.
- `UPLOAD_SPEC_DECISION.md` locks the first implementation to PDF-only upload with decoupled
  chunking.
- `INGEST_ADDITIONS.md` was retired after its upload table and sidecar sections were merged into
  `INGEST.md`.
