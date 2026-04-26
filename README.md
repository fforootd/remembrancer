# Remembrancer

Remembrancer is a local-first personal evidence archive. The MVP starts by
building the artifact spine before any chatbot, embeddings, or autonomous
actions.

The initial product and architecture brief is preserved in
`docs/design/personal_librarian_codex_design_guide.md`.

## Milestone 0

This repository currently contains the service skeleton:

- YAML configuration with local development defaults.
- SQLite migration plumbing.
- A localhost HTTP server.
- A health endpoint.
- A server-rendered HTML shell.

## Run Locally

```sh
go run ./cmd/remembrancer serve --config config/example.yaml
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

Remembrancer is licensed under the GNU Affero General Public License v3.0.
See `LICENSE`.
