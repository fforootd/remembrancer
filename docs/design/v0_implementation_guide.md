# Remembrancer v0 Implementation Guide

Status: canonical Codex implementation guide for the next vertical slice
Last revised: 2026-04-26

This guide translates the product direction in `remembrancer_design_guide.md`
into concrete implementation defaults. It supersedes the older milestone plan in
`docs/design/archive/personal_librarian_codex_design_guide.md`.

## 1. v0 Goal

v0 proves one thing:

> Remembrancer can ingest a local mail archive, preserve originals, extract
> searchable text, and generate a source-linked weekly briefing the user wants to
> read.

The v0 source is a local MBOX file, preferably produced by Google Takeout. This
is a deterministic test and backfill path only. It is not the long-term mail
strategy. Later production ingestors may use IMAP, Gmail API, Apple Mail export,
Maildir, an owned mail server, or an inbound archive mailbox.

## 2. Non-Goals

Do not implement these in v0:

- live mailbox connection.
- source mailbox mutation.
- sending mail or creating external drafts.
- watch folder ingestion.
- proposals, memory, reminders, tasks.
- entities, collections, graph relationships.
- evidence span tables.
- embeddings or vector search.
- multi-user permissions.
- cloud LLM calls by default.

## 3. User-Facing v0 Flow

The first useful flow:

```text
open Remembrancer
  -> import local MBOX file
  -> see imported messages as artifacts
  -> search message text
  -> open an artifact view
  -> choose a seven-day briefing period
  -> generate briefing
  -> read grouped briefing items
  -> open cited artifacts from each item
```

The briefing is the product hook. Search and artifact view are supporting tools.

## 4. Storage Layout

Use the existing configured runtime/archive paths.

Development defaults:

```text
.local/runtime/users/florian/main.sqlite
.local/archive/objects/sha256/<2>/<2>/<hash>
.local/archive/exports/
.local/archive/backups/
```

Raw RFC822 message bytes must be written to the object store before extraction.
Never mutate or delete source MBOX files. Never store live SQLite directly on a
network filesystem.

## 5. v0 Schema

Add only the tables needed for MBOX import, search, and persisted briefings.
Target-state tables remain deferred.

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

- `type = 'email'`
- `source = 'mbox'`

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

## 6. MBOX Import Contract

The importer accepts one local MBOX path.

For each message:

1. Parse one RFC822 message from the MBOX.
2. Compute SHA-256 over the raw RFC822 bytes.
3. Store the raw bytes as a content-addressed blob.
4. Create or reuse the `blob` row.
5. Create or reuse an `artifact` row.
6. Extract metadata:
   - `Message-ID`
   - `Subject`
   - `From`
   - `To`
   - `Cc`
   - `Date`
   - attachment filenames if easy
   - headers needed for deterministic source identity
7. Store metadata in `artifact.metadata_json`.
8. Extract body text:
   - prefer `text/plain`.
   - fall back to HTML-to-text for `text/html`.
   - ignore remote resources.
   - do not execute scripts or fetch URLs.
9. Store `extracted_text`.
10. Upsert `search_document`.
11. Rebuild or update `artifact_fts` for that artifact.

Deduplication:

- Blob dedupe is by raw content hash.
- Artifact dedupe is by `source = 'mbox'` plus a stable `source_id`.
- Use `Message-ID` as `source_id` when present.
- If `Message-ID` is missing, derive `source_id` from the raw content hash.

Import is read-only with respect to the source MBOX. The app never writes to
mail providers, mailboxes, or source archive files.

## 7. Search Contract

v0 search uses SQLite FTS5 only.

Search results should include:

- artifact ID.
- title or subject.
- event date.
- sender if available in metadata.
- a snippet from FTS or extracted text.

Search is a supporting workflow. It does not need natural-language Q&A in v0.

## 8. Artifact View Contract

The artifact view for v0 email artifacts shows:

- subject/title.
- sender, recipients, and date from metadata.
- raw message blob hash.
- extracted text.
- source metadata JSON in a readable section.
- links back to search or briefing where relevant.

Do not render raw HTML email as trusted HTML. Show text extraction first. Any
future HTML preview must be sanitized and isolated.

## 9. Briefing Candidate Selection

Do not hand the last seven days of raw artifacts directly to the LLM. The server
must nominate candidates first.

Candidate input:

- artifacts with `event_at >= period_start` and `event_at < period_end`.
- extracted text and metadata.
- optional FTS snippets around matched signal words.

Initial scoring signals:

Score up:

- attachment filenames are present.
- PDF-ish attachment filenames are present.
- money words: invoice, bill, payment, paid, receipt, refund, statement,
  balance, subscription, renewal.
- action words: action required, please sign, return by, due, deadline, expires,
  appointment, confirm, complete, submit.
- household words: school, teacher, parent, doctor, dentist, insurance, vehicle,
  registration, tax, mortgage, rent, utility.
- date-like language appears near action words.

Score down:

- list or bulk indicators such as `List-Unsubscribe`, `Precedence: bulk`, or
  marketing-like headers.
- newsletter words: unsubscribe, sale, promotion, webinar, digest.
- very short messages with no attachment and no action signals.
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
- candidate list with artifact IDs, titles, dates, metadata summaries, scores,
  and short snippets.

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

If no local LLM is configured, v0 should still support import, artifact view,
search, and deterministic candidate listing. It may render a "candidate briefing"
without generated prose.

## 11. UI Contract

Use server-rendered HTML and HTMX.

v0 screens:

- import screen for a local MBOX path.
- import result screen with counts and errors.
- artifact list/search.
- artifact detail.
- briefing generator form with period start/end.
- briefing detail screen with grouped items and source links.

Keep forms boring. Prefer normal POST requests that also work without complex
client-side state.

## 12. Fixtures

Personal data must never be committed.

Public fixtures:

```text
testdata/public/mail/synthetic_school_form.mbox
testdata/public/mail/synthetic_bill.mbox
testdata/public/mail/synthetic_newsletter.mbox
testdata/public/mail/synthetic_receipt.mbox
```

Private fixtures, ignored by git:

```text
testdata/private/mail/google-takeout/*.mbox
testdata/private/mail/apple-mail/**/*.emlx
```

Importer tests must run against public synthetic fixtures. Manual local testing
may use private fixtures.

## 13. Tests And Acceptance

Required tests for v0:

- migration applies all v0 tables idempotently.
- MBOX parser imports synthetic messages.
- raw RFC822 bytes are stored before extraction.
- duplicate import does not duplicate blobs or artifacts.
- text/plain body extraction works.
- HTML-to-text body extraction works without fetching remote resources.
- FTS search finds imported message text.
- candidate scoring ranks action/bill/school messages above newsletters.
- LLM JSON validation rejects uncited artifact IDs.
- briefing persistence links every item to at least one artifact.
- artifact view never renders raw HTML email as trusted HTML.

Manual acceptance:

1. Import a Google Takeout MBOX from `testdata/private/mail/google-takeout/`.
2. Generate a briefing for a known seven-day window.
3. Confirm every briefing item links to an artifact.
4. Open at least three cited artifacts from the briefing.
5. Search for text from a known message and open the result.
6. Run `go test ./...`.

## 14. Rollout Discipline

Do not expand ingestion until the MBOX briefing loop works.

Do not add memory until the user reads briefings repeatedly.

Do not add reminders until memory or briefing items produce source-linked
actions worth approving.

Do not add external writes until the internal archive and approval model are
trusted.

The next good implementation slice after Milestone 0 is not "all of v1." It is:

```text
MBOX import
  -> artifact/blob/extracted_text/search tables
  -> search UI
  -> artifact view
  -> candidate-scored briefing
  -> persisted briefing with source links
```
