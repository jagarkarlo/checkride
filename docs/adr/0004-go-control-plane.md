# 4. Go control plane and interactive Studio

- Status: accepted

## Context

Checkride needs a long-running control plane for interactive drill management,
while its first implementation is a Python CLI, declarative spec and write
ledger. The planned Studio is an operational interface with live drill state,
timelines, dependency graphs and evidence review, not a content-first site.

## Decision

- Build the HTTP control plane in Go, starting with the standard library and
  keeping the first server dependency-free.
- Migrate backend behavior in tested slices. The existing Python CLI, spec
  validation and ledger remain supported until each behavior has a verified Go
  replacement; do not duplicate or silently change their semantics.
- Keep the Studio recommendation at React and TypeScript. Use Astro only if a
  future public documentation/content site warrants it; defer the Studio build
  tool choice until its first UI slice.
- Treat health/readiness probes, bounded HTTP timeouts, graceful shutdown,
  race-tested code and static checks as baseline controls, not proof of
  high-demand capacity or production resilience.

## Consequences

- Go adds a second implementation language, but provides a small deployable
  control-plane binary and explicit concurrency primitives.
- Python and Go behavior must remain contract-tested during the migration.
- Before production claims, the API needs durable drill execution, bounded
  concurrency and queues, authentication, dependency-aware readiness,
  observability, load tests and documented recovery behavior.
- React is a better initial fit for the highly interactive Studio than Astro's
  content-first defaults; this remains open to revisiting with a concrete UI
  workflow.