# Internal Package Guidelines

These instructions apply to all Go code under `internal`.

## Package Boundaries

- Keep `cmd/zora` thin and put application behavior in focused internal
  packages.
- Avoid package cycles and broad "utility" packages. Prefer small interfaces at
  the call site when they make testing or external service boundaries clearer.
- Do not expose more API surface than the current feature needs.

## Data And State

- SQLite access should be context-aware and explicit about transactions. Roll
  back on errors and wrap commit failures with useful context.
- Migrations should be forward-only, deterministic, and covered by tests when
  schema behavior changes.
- Runtime state belongs under configured runtime/archive paths, not in repo
  paths. Tests should use temporary directories and isolated databases.

## Ingest, Extraction, Jobs, And Pipeline

- Ingest stages should remain idempotent and retryable.
- Watch-folder ingestion is read-only with respect to source files.
- Preserve content-addressed raw blobs; derived text, chunks, facts, and briefing
  items must be rebuildable from source artifacts.
- External systems such as Docling and Ollama should be behind small interfaces
  or clients with context, timeouts, and focused tests.
- Validate and bound LLM outputs before storing them. Require source references
  for generated claims and keep unverified output visibly separate from trusted
  state.

## Go Review Checklist

- `context.Context` is the first argument where cancellation or deadlines matter.
- Errors are checked, wrapped with `%w`, and kept lowercase when intended for
  composition.
- Files, rows, transactions, and HTTP response bodies are closed.
- Goroutine lifetimes are obvious and tied to context or server shutdown.
- Tests cover success, failure, retry/idempotency, and boundary conditions.
