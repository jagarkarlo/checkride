# 3. Technology stack and pinned lab versions

- Status: accepted

## Context

Checkride orchestrates clusters, runs statistics on drill results and needs an
interactive web UI. Drill timings are only comparable when component versions
stay fixed.

## Decision

- Python 3.12+ for the CLI, drill-spec validation and the initial write ledger,
  using Pydantic for specs. The HTTP control plane is implemented in Go; see
  [ADR 0004](0004-go-control-plane.md).
- TypeScript and React for the Studio web UI.
- PostgreSQL to store drill results; the ledger itself is a local SQLite file
  kept outside the clusters under test.
- The lab uses K3s v1.35.8 through k3d. Kubernetes 1.35 is inside the
  CloudNativePG 1.30 support window and among the versions Velero 1.18 is
  tested on.

## Consequences

- Two languages to maintain instead of one.
- Version upgrades are deliberate: every result records the versions it ran
  with, and upgrading the lab means re-running baseline drills.
