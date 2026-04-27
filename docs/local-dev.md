# Local Development

Zora's default development stack is native, not Docker Compose. The Go server
runs as a normal local process, Docling runs out-of-process from a managed Python
virtual environment, and all development data stays under `.local/`.

## Quick Start

From a fresh checkout:

```sh
make setup
make llm-pull   # optional, needed for generated action items
make dev
```

`make setup` dispatches to the host-specific bootstrap:

- macOS: installs the `Brewfile`, including Ollama, and creates `.local/docling`.
- Ubuntu/Debian: installs apt packages, creates the `zora` user and runtime
  directories, installs Ollama unless `ZORA_SKIP_OLLAMA_SETUP=1`, installs
  systemd units, and creates `/opt/zora/docling`.

`make llm-pull` downloads `${ZORA_LLM_MODEL:-gemma4:e2b-it-q4_K_M}`. It is skipped
by setup so first install stays quick and does not immediately consume several
GB of disk.

`make dev` starts Docling, Ollama, and Zora together, waits for their health
endpoints, and prints the URLs. It reuses an already-running service if the
expected local URL is already reachable.

Open:

- Zora UI: `http://127.0.0.1:8787/`
- Zora health: `http://127.0.0.1:8787/healthz`
- Zora action items: `http://127.0.0.1:8787/action-items`
- Docling API docs: `http://127.0.0.1:5001/docs`
- Docling UI: `http://127.0.0.1:5001/ui`
- Ollama API: `http://127.0.0.1:11434/api/tags`

## Daily Loop

The most useful commands are:

```sh
make doctor
make test
make run-docling
make run-ollama
make run-zora
make llm-pull
make scan
make package-check
make package-snapshot
```

Use `make run-docling`, `make run-ollama`, and `make run-zora` in separate
terminals when you want cleaner logs for each service. Use `make dev` when you
just want the whole local stack running.

## Ingest A File

Drop supported files into `.local/inbox`:

- `.pdf`
- `.png`, `.jpg`, `.jpeg`, `.tif`, `.tiff`
- `.txt`, `.md`

The scanner waits for `ingest.settle_duration` from `config/example.yaml` before
queueing a changed file. The default is `10s`. To ask a running server to scan
immediately:

```sh
make scan
```

Text and Markdown files are read directly by Go. PDFs and images are sent to
Docling at `extract.docling.base_url`, which defaults to
`http://127.0.0.1:5001`.

## Local Data

The development config writes to:

- Runtime state: `.local/runtime`
- SQLite database: `.local/runtime/users/florian/main.sqlite`
- Original artifact archive: `.local/archive`
- Watch folder: `.local/inbox`
- Docling virtual environment: `.local/docling`

Those paths are ignored by git. If you intentionally want a blank local archive,
stop Zora and remove the relevant `.local/` subdirectories before restarting.

## Configuration

Local development uses `config/example.yaml` by default. To run with a different
config:

```sh
ZORA_CONFIG=/path/to/config.yaml make run-zora
```

For Docling port experiments:

```sh
DOCLING_PORT=5010 make run-docling
```

For the combined supervisor:

```sh
DOCLING_PORT=5010 DOCLING_BASE_URL=http://127.0.0.1:5010 make dev
```

If you change Docling's port, update `extract.docling.base_url` in the active
Zora config too.

Local development enables `llm.enabled` in `config/example.yaml` and uses Ollama
at `http://127.0.0.1:11434` with `gemma4:e2b-it-q4_K_M`. The dev config gives
generation a longer timeout because the first request may cold-load the model.
To use a different model for make targets:

```sh
ZORA_LLM_MODEL=gemma4:e4b make llm-pull
ZORA_LLM_MODEL=gemma4:e4b make dev
```

For a lighter Qwen comparison, override the model with `qwen3.5:4b-q4_K_M`.
For a slower Qwen quality-mode pass, use `qwen3.5:9b-q4_K_M`. Update
`llm.model` in the active config to match whichever model you run.

## Doctor

Run:

```sh
make doctor
```

The doctor checks required commands, optional packaging tools, local runtime
directories, the Docling venv, health URLs, Ollama, and the default local model.
To fail on warnings and run the Go test suite:

```sh
scripts/doctor.sh --strict --test
```

## Packaging Loop

Before creating local release artifacts:

```sh
make test
make package-check
make package-snapshot
```

Snapshot artifacts are written to `dist/`. The `.deb` installs the Zora binary,
default config, and systemd units, but it does not bundle Docling's Python or ML
dependencies. Install Docling with the Ubuntu bootstrap before starting
`docling-serve.service`.

## Troubleshooting

If Docling is not reachable, run `make run-docling` directly and watch its logs.
The first start can be slow while Python packages or model artifacts settle.

If Ollama is not reachable, run `make run-ollama` directly and watch its logs.
If Ollama is reachable but action-item generation complains about a missing
model, run `make llm-pull`.
If generation times out near the configured `llm.timeout`, restart `make dev`
after increasing that value in the active config, then retry from `/action-items`.

If Zora is not reachable, check whether port `8787` is already in use, then run
`make run-zora` directly.

If PDFs/images keep retrying and then dead-letter, confirm that the Docling URL in
the UI matches `extract.docling.base_url` in the active config. Text/Markdown
ingest does not require Docling.

If tests accidentally use cgo, run the canonical command:

```sh
CGO_ENABLED=0 go test ./...
```
