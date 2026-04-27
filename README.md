# Zora

> **Note:** This is currently a personal research project built in my free time.

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

The first durable ingest slice is implemented:

- YAML configuration with local development defaults.
- SQLite migration plumbing.
- A localhost HTTP server.
- A health endpoint.
- A server-rendered HTML shell.
- Queue-backed watch folder ingest.
- Docling-backed extraction for PDFs and images.
- GoReleaser packaging for native binary and `.deb` artifacts.

## Native Setup

Zora is intended to run as a native local service. The Go binary stays CGO-free,
Docling runs out-of-process in a Python virtual environment, and SQLite/files live
on the host filesystem.

For the full local workflow, see `docs/local-dev.md`.

On macOS:

```sh
make setup-macos
```

On Ubuntu:

```sh
make setup-ubuntu
```

The macOS bootstrap uses `Brewfile`, including Ollama. The Ubuntu bootstrap uses
`apt` for host packages, installs Ollama unless `ZORA_SKIP_OLLAMA_SETUP=1`, and
installs `docling-serve[ui]` into `/opt/zora/docling`.

## Run Locally

Optionally pull the default local model before generating action items:

```sh
make llm-pull
```

```sh
make dev
```

Then open:

- `http://127.0.0.1:8787/`
- `http://127.0.0.1:8787/healthz`
- `http://127.0.0.1:8787/action-items`
- `http://127.0.0.1:5001/docs`
- `http://127.0.0.1:5001/ui`

The example config writes local development state under `.local/`, which is
ignored by git.

For separate service logs, run `make run-docling`, `make run-ollama`, and
`make run-zora` in separate terminals.

Local dev defaults to the fast `gemma4:e2b-it-q4_K_M` model. For a slower but
stronger local ingest pass on 16GB+ Macs, use `qwen3.5:9b-q4_K_M` and update
`llm.model` in your active config.

## Package

GoReleaser builds local release artifacts:

```sh
make test
make package-check
make package-snapshot
```

Snapshot output is written to `dist/`, including macOS/Linux tarballs,
checksums, and Ubuntu `.deb` packages. The packaged Ubuntu defaults are:

- Config: `/etc/zora/config.yaml`
- Data: `/var/lib/zora`
- Binary: `/usr/bin/zora`
- Services: `zora.service` and `docling-serve.service`

Docling is not bundled into the Zora package. Install it with the Ubuntu
bootstrap before starting `docling-serve.service`.

Check build metadata with:

```sh
make version
```

## Test

```sh
make test
```

## License

Zora is licensed under the GNU Affero General Public License v3.0.
See `LICENSE`.
