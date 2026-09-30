---
title: DrillRun evidence schema & report reference
description: Reference documentation for recorded DrillRun evidence documents and the computed verification report.
---

Checkride strictly distinguishes between what you intend to test (`Drill`) and what was observed during an actual test run (`DrillRun`).

## `DrillRun` schema (`checkride/v1alpha1`)

A `DrillRun` is an audit-ready JSON document capturing timing, execution phases, check outcomes, and write ledger state. The versioned JSON Schema is served by the API at `GET /api/v1/schemas/drillrun`.

### Document structure

```json
{
  "apiVersion": "checkride/v1alpha1",
  "kind": "DrillRun",
  "metadata": {
    "name": "mlflow-namespace-loss-20261001",
    "drill": "mlflow-namespace-loss"
  },
  "spec": {
    "scenario": "namespace-loss",
    "upTo": "V4",
    "v4Evidence": { "invariants": ["every-run-has-an-experiment"] },
    "objectives": { "rto": "15m", "rpo": "5m" }
  },
  "status": {
    "failureAt": "2026-10-01T10:00:00Z",
    "completedAt": "2026-10-01T10:11:43Z",
    "phases": [
      {
        "name": "provision-restore-cluster",
        "startedAt": "2026-10-01T10:00:42Z",
        "endedAt": "2026-10-01T10:03:18Z"
      }
    ],
    "checks": [
      {
        "level": "V0",
        "name": "base backup completed",
        "passed": true,
        "detail": "barman: backup 20261001T0900 COMPLETED"
      }
    ],
    "ledger": {
      "acks": [
        { "writeId": "run-00001", "ackedAt": "2026-10-01T09:50:00.500Z" }
      ],
      "present": ["run-00001"]
    }
  }
}
```

### Key constraints
- **`status.failureAt` & `status.completedAt`:** High-precision ISO 8601 UTC timestamps. RTO is strictly calculated from failure injection to completion time; uninstrumented gaps are flagged.
- **`status.phases`:** Up to 64 ordered recovery intervals.
- **`status.checks`:** Up to 256 individual assertions tagged with their respective verification level (`V0` to `V4`).
- **`status.ledger`:** Up to 200,000 acknowledged writes and restored database IDs.

---

## Computed report structure

Passing a `DrillRun` document to `POST /api/v1/runs/report` or `checkride-report` outputs an evaluated report object:

| Field | Type | Description |
| --- | --- | --- |
| `verdict` | string | Overall evaluation: `verified`, `failed`, or `incomplete`. |
| `headline` | string | Human-readable executive summary of the result. |
| `requestedLevel` | string | Target verification depth (`V0`–`V4`). |
| `deepestPassed` | string/null | Highest level reached where all lower levels also passed. |
| `firstFailed` | string/null | Lowest level that encountered a failing check. |
| `rto` | object/null | Recovery time breakdown, slowest phase, and objective status. |
| `rpo` | object/null | Mathematical data loss window, holes count, and timeline buckets. |
| `findings` | array | Prioritized list of `error`, `warning`, and `info` diagnostics. |

### Evaluation rules

1. **Contiguous depth:** A drill is only verified to level $N$ if every level from $V_0$ up to $N$ passed. A failed check at $V_2$ prevents claiming $V_3$ or $V_4$, even if data assertions succeeded.
2. **Strict V4 requirements:** Level V4 requires either an external write ledger or application invariants with matching checks.
3. **Ledger consistency:** The presence of a "hole" (an older acknowledged write missing while a newer write is restored) fails V4 automatically.
4. **Objective enforcement:** If an RTO or RPO objective is specified, exceeding the threshold fails the drill. If an objective is configured but no measurements are present, the verdict is `incomplete`.
