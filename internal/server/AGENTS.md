# Server Guidelines

These instructions apply to `internal/server`.

## Rendering And UI

- Zora's UI is server-rendered HTML. Use the existing `html/template` flow,
  templates, and static assets.
- Do not introduce a JavaScript framework unless the task explicitly calls for a
  frontend architecture change.
- Preserve accessibility basics: semantic headings, labels for controls, useful
  link text, and forms that work without client-side JavaScript.
- Match existing Pico/brand styling and keep pages quiet, dense, and useful.

## Safety

- Treat artifact text, Markdown, OCR output, filenames, snippets, and LLM text as
  untrusted data.
- Do not render extracted content as trusted HTML. Use the existing sanitization
  and escaping path for Markdown previews.
- Keep source citations visible for generated or derived claims.
- Avoid adding handlers that mutate external systems or durable trusted state
  without an explicit user action.

## Handlers And Tests

- Keep handlers small; push database, artifact, and action-item behavior into
  internal packages where practical.
- Use request contexts for DB and service calls.
- Prefer `httptest` handler tests with explicit status, redirect, and rendered
  content assertions.
- Cover disabled-service and degraded-mode behavior for LLM, Docling, and local
  data dependencies.
