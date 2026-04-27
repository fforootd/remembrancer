# Documentation Guidelines

These instructions apply to documentation under `docs`.

## Sources Of Truth

- `DESIGN.md` is the navigable design index at the repository root.
- `docs/design/zora_design_guide.md` is the canonical product guide.
- `docs/design/v0_implementation_guide.md` is the concrete v0 implementation
  guide.
- `docs/local-dev.md` is the canonical local development workflow.
- If `DESIGN.md` conflicts with a canonical deep guide, update the relevant docs
  so the conflict is resolved instead of adding another competing statement.

## Writing Style

- Keep docs direct, operational, and grounded in current repo behavior.
- Distinguish current v0 behavior from future roadmap or target architecture.
- Preserve Zora's core product language: local-first, immutable evidence,
  source-grounded interpretation, human approval for trusted state, and no
  autonomous external writes.
- Prefer command examples that use existing `make` targets.
- Do not document private local paths, personal data, secrets, or host-specific
  details beyond the examples already committed.
