# Zora v0 Implementation Guide

Status: canonical Codex implementation guide for the next vertical slice
Last revised: 2026-04-26

This guide translates the product direction in `zora_design_guide.md` into
concrete implementation defaults.

## 1. v0 Goal

v0 proves one thing:

> Zora can ingest a local watch folder, preserve originals, extract searchable
> text, and generate a source-linked weekly briefing the user wants to read.

The v0 source is a single local watch folder on disk. The user drops files into
it: scanned PDFs, images, plain text or Markdown notes, optionally `.eml` files
exported from a mail client. This is the chosen realization of the product
guide's "one ingestion source" rule for v0. Live mail connectors, IMAP, Gmail
API, Apple Mail integration, and other ingestors are out of scope until the
watch-folder briefing loop is loved.

## 2. Non-Goals

Do not implement these in v0:

- live mail connectors (IMAP, Gmail API, Apple Mail).
- any ingestion source other than the configured watch folder.
- mutating or deleting source files in the watch folder.
- sending mail or creating external drafts.
- proposals, memory, reminders, tasks.
- entities, collections, graph relationships.
- evidence span tables.
- embeddings or vector search.
- multi-user permissions.
- cloud LLM calls by default.

## 3. User-Facing v0 Flow

The first useful flow:

```text
open Zora
  -> see configured watch folder and recent ingestion status
  -> drop files into the watch folder
  -> see new files appear as artifacts
  -> search artifact text
  -> open an artifact view
  -> choose a seven-day briefing period
  -> generate briefing
  -> read grouped briefing items
  -> open cited artifacts from each item
```

The briefing is the product hook. Search and the artifact view are supporting
tools.

## 4. Storage Layout

Use the existing configured runtime, archive, and inbox paths.

Development defaults:

```text
.local/inbox/                                          (the watch folder)
.local/runtime/users/florian/main.sqlite
.local/archive/objects/sha256/<2>/<2>/<hash>
.local/archive/exports/
.local/archive/backups/
```

Raw file bytes must be written to the content-addressed object store before
extraction. Never mutate or delete files in the watch folder. Never store live
SQLite directly on a network filesystem.

## 5. v0 Schema

Add only the tables needed for watch-folder ingestion, search, and persisted
briefings. Target-state tables remain deferred.

```sql
CREATE TABLE blob (
  hash TEXT PRIMARY KEY,
  algorithm TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  mime_type TEXT,
  storage_path TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE artifact (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  source TEXT NOT NULL,
  source_id TEXT,
  title TEXT,
  owner TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  event_at TEXT,
  metadata_json TEXT,
  created_at TEXT NOT NULL,
  deleted_at TEXT,
  FOREIGN KEY (content_hash) REFERENCES blob(hash)
);

CREATE UNIQUE INDEX artifact_source_source_id_idx
ON artifact(source, source_id)
WHERE source_id IS NOT NULL;

CREATE INDEX artifact_event_at_idx ON artifact(event_at);

CREATE TABLE extracted_text (
  artifact_id TEXT PRIMARY KEY,
  text TEXT NOT NULL,
  extractor TEXT NOT NULL,
  extractor_version TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE TABLE search_document (
  rowid INTEGER PRIMARY KEY,
  artifact_id TEXT NOT NULL UNIQUE,
  title TEXT,
  text TEXT,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);

CREATE VIRTUAL TABLE artifact_fts USING fts5(
  title,
  text,
  content='search_document',
  content_rowid='rowid',
  tokenize='unicode61 remove_diacritics 2'
);

CREATE TABLE briefing (
  id TEXT PRIMARY KEY,
  period_start TEXT NOT NULL,
  period_end TEXT NOT NULL,
  title TEXT NOT NULL,
  source_query_json TEXT NOT NULL,
  model_name TEXT,
  prompt_version TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE briefing_item (
  id TEXT PRIMARY KEY,
  briefing_id TEXT NOT NULL,
  category TEXT NOT NULL,
  title TEXT NOT NULL,
  summary TEXT NOT NULL,
  why_it_matters TEXT,
  action_text TEXT,
  due_at TEXT,
  confidence REAL,
  source_status TEXT NOT NULL,
  sort_order INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (briefing_id) REFERENCES briefing(id)
);

CREATE TABLE briefing_item_artifact (
  briefing_item_id TEXT NOT NULL,
  artifact_id TEXT NOT NULL,
  snippet TEXT,
  PRIMARY KEY (briefing_item_id, artifact_id),
  FOREIGN KEY (briefing_item_id) REFERENCES briefing_item(id),
  FOREIGN KEY (artifact_id) REFERENCES artifact(id)
);
```

Allowed v0 artifact values:

- `source = 'watch_folder'`
- `type` in `{'pdf', 'image', 'text', 'email'}`. `email` only when the user
  drops `.eml` files into the watch folder.

Allowed v0 briefing categories:

- `needs_action`
- `bills_money`
- `school_family`
- `travel_events`
- `house_car`
- `documents_to_file`
- `interesting`
- `unverified`

Allowed `briefing_item.source_status` values:

- `verified`: all cited artifacts exist and were in candidate context.
- `unverified`: item was useful enough to show, but citation snippets could not
  be mechanically matched.

Invalid uncited items are not persisted.

## 6. Watch Folder Ingestion Contract

The ingestor scans the configured watch folder (default `.local/inbox`) and
processes any file it has not yet ingested.

For each new file:

1. Read the raw bytes from disk without modifying the file.
2. Compute SHA-256 over the raw bytes.
3. Store the raw bytes as a content-addressed blob under the archive object
   store.
4. Create or reuse the `blob` row. Detect MIME via file extension first, sniff
   if needed.
5. Create or reuse an `artifact` row with:
   - `source = 'watch_folder'`
   - `source_id` derived from the absolute file path plus the content hash so
     the same file in the same place dedupes deterministically across rescans.
   - `type` chosen from `pdf`, `image`, `text`, `email` based on extension/MIME.
   - `captured_at = now()`.
   - `event_at` initialised from file mtime; later extractors may refine this
     using dates parsed from the content.
   - `title` initialised from the filename (without extension); extractors may
     refine.
6. Extract text by type:
   - `text` (`.txt`, `.md`): read UTF-8 directly. Level 0 (built into the Go
     binary).
   - `email` (`.eml`): parse RFC822, prefer `text/plain`, fall back to
     HTML-to-text. Never fetch remote resources. Never execute scripts. Level 0.
   - `pdf` (`.pdf`): invoke external `pdftotext` (poppler). Required v0
     dependency. Level 1 (optional external tool that v0 hard-depends on).
   - `image` (`.png`, `.jpg`, `.jpeg`, `.tiff`): invoke external Tesseract OCR.
     Level 1.
7. Persist `extracted_text` with `extractor` set to the tool used.
8. Upsert `search_document`.
9. Rebuild or update `artifact_fts` for that artifact.

Deduplication:

- Blob dedupe is by raw content hash.
- Artifact dedupe is by `source = 'watch_folder'` plus the deterministic
  `source_id` above. The same file content under a different path produces a
  new artifact (same blob, different artifact). The same file at the same path
  on rescan does not duplicate.

Ingestion is read-only with respect to the watch folder. The app never moves,
renames, deletes, or modifies files there.

## 7. Search Contract

v0 search uses SQLite FTS5 only.

Search results should include:

- artifact ID.
- title (filename stem, refined by extractor where available).
- artifact `type`.
- event date.
- a snippet from FTS or extracted text.

Search is a supporting workflow. It does not need natural-language Q&A in v0.

## 8. Artifact View Contract

The artifact view is type-aware.

For all types:

- title.
- artifact `type`, source, content hash.
- captured_at and event_at timestamps.
- extracted text.
- source metadata JSON in a readable section.
- links back to search or briefing where relevant.

Type-specific additions:

- `pdf`: optional thumbnail (Level 1, optional in v0).
- `image`: original image displayed inline, served from a sandboxed handler
  that never references external resources.
- `email`: parsed headers (Subject, From, To, Cc, Date) shown above the
  extracted text body. Never render raw HTML email as trusted HTML. Any future
  HTML preview must be sanitized and isolated.
- `text`: render extracted text as preformatted text or basic Markdown. No
  remote resource loading.

## 9. Briefing Candidate Selection

Do not hand the last seven days of raw artifacts directly to the LLM. The
server must nominate candidates first.

Candidate input:

- artifacts with `event_at >= period_start` and `event_at < period_end`.
- extracted text and metadata.
- optional FTS snippets around matched signal words.

Initial scoring signals.

Score up:

- recently captured files with substantial extracted text.
- money words: invoice, bill, payment, paid, receipt, refund, statement,
  balance, subscription, renewal.
- action words: action required, please sign, return by, due, deadline,
  expires, appointment, confirm, complete, submit.
- household words: school, teacher, parent, doctor, dentist, insurance,
  vehicle, registration, tax, mortgage, rent, utility.
- date-like language appears near action words.
- artifact `type = pdf` (forms and statements are common signal carriers).
- artifact `type = image` with non-trivial OCR text.

Score down:

- extracted text under a small minimum length (e.g. < 80 characters) with no
  date-like language.
- generic filenames such as `IMG_1234.jpg` with no OCR signal.
- newsletter-like vocabulary: unsubscribe, sale, promotion, webinar, digest.
- bulk indicators if present in `.eml` headers (`List-Unsubscribe`,
  `Precedence: bulk`).
- sender domains explicitly marked noisy in a future local config.

Default candidate limit:

- take the top 40 candidates for the selected week.
- if fewer than 40 exist, include all with non-negative score.
- if no candidates survive, generate a briefing that says no strong candidates
  were found and still links to the search view for the period.

Candidate scoring must be deterministic and testable without an LLM.

## 10. Briefing LLM Contract

The LLM is an editor, not the primary selector.

Input:

- period start and end.
- prompt version.
- candidate list with artifact IDs, titles, types, dates, metadata summaries,
  scores, and short snippets.

Output must be JSON:

```json
{
  "items": [
    {
      "category": "needs_action",
      "title": "Submit school form",
      "summary": "A form appears to require a parent signature.",
      "why_it_matters": "The form mentions a submission deadline.",
      "action_text": "Review and submit the form.",
      "artifact_ids": ["artifact_123"],
      "evidence_snippets": [
        {
          "artifact_id": "artifact_123",
          "quote": "Please return this form by May 10."
        }
      ],
      "due_at": "2026-05-10",
      "confidence": 0.82
    }
  ]
}
```

Validation:

- `items` must be an array.
- every item must include at least one `artifact_id`.
- every `artifact_id` must exist in the candidate input set.
- every `evidence_snippet.artifact_id` must be one of the item's artifact IDs.
- if an evidence quote is provided, it should appear in extracted text after
  whitespace-normalized matching.
- invalid uncited items are dropped.
- useful items with valid artifact IDs but unmatched quotes are persisted as
  `source_status = 'unverified'` and category `unverified` if needed.

Persist the briefing and accepted items before rendering the briefing screen.

If no local LLM is configured, v0 should still support ingestion, the artifact
view, search, and deterministic candidate listing. It may render a "candidate
briefing" without generated prose.

## 11. UI Contract

Use server-rendered HTML and HTMX.

v0 screens:

- watch folder status: configured path, file count, last scan time, recent
  ingestion errors.
- artifact list and search.
- artifact detail (type-aware per Section 8).
- briefing generator form with period start/end.
- briefing detail screen with grouped items and source links.

Keep forms boring. Prefer normal POST requests that also work without complex
client-side state.

## 12. Fixtures

Personal data must never be committed.

Public fixtures live in `testdata/public/inbox/` and are file-shaped:

```text
testdata/public/inbox/synthetic_school_form.pdf
testdata/public/inbox/synthetic_bill.pdf
testdata/public/inbox/synthetic_receipt.pdf
testdata/public/inbox/synthetic_newsletter.txt
testdata/public/inbox/synthetic_scan.png
testdata/public/inbox/synthetic_school_email.eml
```

The PNG fixture should contain OCR-able text. The `.eml` fixture exercises the
optional email path.

Private fixtures, ignored by git:

```text
testdata/private/inbox/**
```

Ingestor tests must run against public synthetic fixtures. Manual local testing
may use private fixtures.

## 13. Tests And Acceptance

Required tests for v0:

- migration applies all v0 tables idempotently.
- watch-folder scan picks up new files and creates artifacts.
- raw bytes are stored as content-addressed blobs before extraction.
- duplicate file content does not duplicate blobs; same file at same path on
  rescan does not duplicate artifacts.
- text/Markdown extraction reads UTF-8 correctly.
- HTML-to-text extraction for `.eml` does not fetch remote resources.
- PDF extraction succeeds on the synthetic PDF fixtures.
- OCR extraction returns expected text on the synthetic image fixture.
- FTS search finds extracted text from each fixture type.
- candidate scoring ranks bill/form/school artifacts above newsletters.
- LLM JSON validation rejects items with uncited or out-of-set artifact IDs.
- briefing persistence links every item to at least one artifact.
- artifact view never renders raw HTML email as trusted HTML.

Manual acceptance:

1. Drop a mix of synthetic fixtures into `.local/inbox/`.
2. Confirm artifacts appear in the artifact list with extracted text.
3. Generate a briefing for a known seven-day window.
4. Confirm every briefing item links to an artifact.
5. Open at least three cited artifacts from the briefing.
6. Search for text from a known fixture and open the result.
7. Run `go test ./...`.

## 14. Rollout Discipline

Do not expand ingestion until the watch-folder briefing loop works.

Do not add memory until the user reads briefings repeatedly.

Do not add reminders until memory or briefing items produce source-linked
actions worth approving.

Do not add external writes until the internal archive and approval model are
trusted.

The next good implementation slice after Milestone 0 is not "all of v1." It is:

```text
watch folder ingestion
  -> artifact/blob/extracted_text/search tables
  -> search UI
  -> artifact view
  -> candidate-scored briefing
  -> persisted briefing with source links
```
