---
title: Write ledger & exact RPO
description: How Checkride measures exact data loss instead of estimating Recovery Point Objective.
---

Most backup vendors quote an RPO based on snapshot schedules: *"We snapshot every 15 minutes, so your RPO is 15 minutes."* In reality, that is only your maximum theoretical data loss window. If a database transaction commits 2 seconds before an ungraceful node crash, did that transaction survive?

Checkride introduces an **acknowledged-write ledger** to measure the exact recovery point down to individual records.

## Ground truth outside the cluster

The write ledger is an append-only log of transactions the application acknowledged to clients. Crucially, the ledger must be maintained **outside the cluster under test**, ensuring that a catastrophic cluster failure does not destroy the ground truth.

Each recorded acknowledgement contains:
- `write_id`: Unique identifier (UUID, sequential transaction ID, or hash).
- `acked_at`: High-resolution ISO 8601 UTC timestamp of client confirmation.

```sql
CREATE TABLE acks (
    write_id TEXT PRIMARY KEY,
    acked_at_us INTEGER NOT NULL
);
```

## How exact RPO is calculated

After restoring the database into the segregated restore cluster, Checkride inspects the restored database and queries for all present write IDs.

```
Expected writes:   [ w001 ] ── [ w002 ] ── [ w003 ] ── [ w004 ] ── [ w005 ]
Restored database: [ w001 ] ── [ w002 ] ── [ w003 ] ──   MISSING ──   MISSING
                                             ▲                         ▲
                                             │                         │
                                      Recovery Point               Failure Time
                                             └──────── RPO ────────────┘
```

The evaluator classifies each write acknowledged at or before `failure_at`:

1. **Recovery Point:** The timestamp of the last write in the longest contiguous sequence of recovered records.
2. **Exact RPO:** The duration between the `failure_at` timestamp and the `Recovery Point`:
   $$\text{RPO} = t_{\text{failure}} - t_{\text{recovery\_point}}$$
3. **Resolution:** The gap between the recovery point and the first missing write. The true point of data loss lies strictly within this interval:
   $$\text{Resolution} = t_{\text{first\_lost}} - t_{\text{recovery\_point}}$$

## Detecting inconsistent restores ("Holes")

In a consistent Point-in-Time Recovery (PITR), transaction logs are replayed sequentially. A restored database should never contain a newer write if an older acknowledged write is missing.

If Checkride detects a write $w_j$ present while an earlier write $w_i$ ($i < j$) is missing, it flags a **hole**:
- **Consistent Restore:** `holes == 0`.
- **Inconsistent Restore:** `holes > 0`. This indicates data corruption, partial partition restore, or silent transaction loss. Inconsistent restores fail V4 verification automatically.

## Recording acknowledgements via CLI

You can populate a ledger using the `checkride` CLI:

```bash
checkride import-acks --ledger run.ledger.db acks.csv
```

To evaluate the exact RPO against restored IDs:

```bash
checkride rpo --ledger run.ledger.db --present restored_ids.txt --failure-at 2026-10-01T10:00:00Z --json
```
