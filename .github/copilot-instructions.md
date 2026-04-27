# Copilot Review Instructions

Review this repository as a Go, SQLite, and server-rendered HTML application for
a local-first personal records system. Prioritize correctness, data safety,
source-grounding, and maintainability over broad rewrites.

## Repository Context

- Go entrypoint: `cmd/zora`.
- Application packages: `internal`.
- Design docs: `DESIGN.md` and `docs/design`.
- Local development docs: `docs/local-dev.md`.
- Canonical checks: `make test`, `CGO_ENABLED=0 go test ./...`,
  `make doctor`, and `make package-check`.

## Highest-Priority Review Concerns

- Preserve local-first behavior and CGO-free packaging unless the PR explicitly
  justifies changing that constraint.
- Raw artifacts are immutable evidence. Flag code that rewrites, deletes, moves,
  or trusts source artifacts unexpectedly.
- Treat extracted artifact text, OCR, Markdown, filenames, and LLM output as
  untrusted data. They must not control prompts, permissions, tools, external
  writes, or trusted durable state.
- LLM output must remain derived and source-grounded. Flag generated claims that
  lack citations, validation, bounded parsing, or visible unverified status.
- No autonomous external writes. Drafting or proposing is allowed; sending,
  deleting, mutating outside systems, or accepting durable trusted state requires
  explicit user action.
- Never commit secrets, personal data, `.local`, `dist`, `tmp`, or
  `testdata/private` contents.

## Go Review Checklist

- Require idiomatic Go: `gofmt`, simple package boundaries, narrow interfaces,
  and standard library patterns where practical.
- `context.Context` should be the first argument for request, DB, filesystem,
  subprocess, and network work.
- Errors should be checked and wrapped with actionable context using `%w`.
  Avoid panics except for startup or compile-time invariant failures.
- Files, rows, transactions, and HTTP response bodies should be closed.
- HTTP clients and external service calls should use timeouts and request
  contexts.
- Tests should be deterministic and use `t.TempDir`, `httptest`, explicit
  fixtures, and fixed clocks when time matters.

## Server And Docs Review Checklist

- UI is server-rendered HTML using existing templates and static assets. Do not
  introduce a JavaScript framework without a clear need.
- Escaping and sanitization matter. Do not render extracted or generated content
  as trusted HTML.
- Documentation should distinguish current behavior from roadmap ideas and keep
  `DESIGN.md`, `docs/design`, and `docs/local-dev.md` consistent.

## Commits And PR Titles

When visible, flag commit messages or PR titles that do not use semantic
Conventional Commit tags such as `feat:`, `fix:`, `docs:`, `test:`,
`refactor:`, `chore:`, `build:`, `ci:`, `perf:`, `style:`, or `revert:`.
