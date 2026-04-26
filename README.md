# Zora

Zora is a local-first records system for personal and household artifacts.
It preserves original evidence, extracts searchable text, and uses AI as an
interpreter over cited sources.

The first product bet is simple:

> The briefing earns the archive.

If Zora cannot produce a weekly briefing worth reading, no amount of memory,
reminders, or assistant behavior will save it. The first real vertical slice
therefore focuses on ingesting a local watch folder, preserving raw artifacts
(PDFs, images, dropped files, optionally exported `.eml` messages), indexing
extracted text, and generating a source-linked briefing for a chosen seven-day
window.

## Design Docs

- `DESIGN.md` is the design index.
- `docs/design/zora_design_guide.md` is the canonical product guide.
- `docs/design/v0_implementation_guide.md` is the concrete v0 implementation guide.

## Current State

Milestone 0 is implemented:

- YAML configuration with local development defaults.
- SQLite migration plumbing.
- A localhost HTTP server.
- A health endpoint.
- A server-rendered HTML shell.

## Run Locally

```sh
go run ./cmd/zora serve --config config/example.yaml
```

Then open:

- `http://127.0.0.1:8787/`
- `http://127.0.0.1:8787/healthz`

The example config writes local development state under `.local/`, which is
ignored by git.

## Test

```sh
go test ./...
```

## License

Zora is licensed under the GNU Affero General Public License v3.0.
See `LICENSE`.
