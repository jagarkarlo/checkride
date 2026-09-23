# 1. Verification levels V0-V4

- Status: accepted

## Context

Backup and restore tools report success at very different depths: a backup
job status, a restore status, healthy pods, or row counts. None of them checks
that writes the application saw acknowledged survived the restore, so a
"successful" restore can still lose or corrupt data.

## Decision

Model verification as five ordered levels:

| Level | Checks |
|---|---|
| V0 | Backup tool status |
| V1 | Restore tool status |
| V2 | Workload health |
| V3 | Structural data checks: tables, row counts, checksums |
| V4 | Data correctness: invariants, acknowledged-write ledger, cross-store consistency |

Every drill reports the deepest level it passed and the first level that
caught a failure. A drill that claims V4 must use the write ledger or at least
one invariant.

## Consequences

- Results from different tools become comparable: Kymaros checks roughly at
  V2, Databasus at V3.
- V4 needs application-specific invariants and instrumented writes, which is
  extra work for users.
