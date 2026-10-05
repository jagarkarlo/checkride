---
title: k3d isolated cluster runbook
description: Step-by-step guide to executing a real PostgreSQL disaster recovery drill across two local k3d clusters.
---

This runbook guides you through provisioning two segregated Kubernetes clusters on your workstation using k3d, executing a PostgreSQL logical backup and restore drill, and verifying the evidence.

## Prerequisites

Ensure the following tools are installed:
- **Docker:** 24.0+ (engine running)
- **k3d:** v5.4+ (`k3d version`)
- **kubectl:** v1.28+
- **Python:** 3.12+ with Checkride installed (`pip install -e .`)
- **Go:** 1.25+ (for `checkride-report`)

## 1. Create the segregated clusters

Checkride bundles declarative cluster definitions in `lab/k3d/source.yaml` and `lab/k3d/restore.yaml`. Run:

```bash
make lab-up
```

Or manually:

```bash
k3d cluster create --config lab/k3d/source.yaml
k3d cluster create --config lab/k3d/restore.yaml
```

This creates:
- `k3d-checkride-source`: Source cluster on dedicated Docker bridge network.
- `k3d-checkride-restore`: Target cluster with isolated API server, network, and storage.

## 2. Proxy and offline image caching (if needed)

If running in corporate or restricted network environments where nodes cannot pull from Docker Hub, import the required images from the host daemon into both clusters:

```bash
docker pull rancher/mirrored-pause:3.10.2
docker pull postgres:16.8

k3d image import -c checkride-source -c checkride-restore \
  rancher/mirrored-pause:3.10.2 \
  postgres:16.8
```

## 3. Verify cluster readiness

Check that both clusters are reachable and have ready nodes:

```bash
checkride lab status
```

Expected output:
```
source   [READY    ] context=k3d-checkride-source nodes=2
restore  [READY    ] context=k3d-checkride-restore nodes=2
```

## 4. Execute the recovery drill

Run the automated PostgreSQL disaster recovery drill:

```bash
checkride lab run --writes 10 --output /tmp/k3d-evidence.json
```

What this does:
1. Validates that `k3d-checkride-source` and `k3d-checkride-restore` address distinct Kubernetes control planes.
2. Creates an ephemeral `checkride-<id>` namespace in both clusters.
3. Launches PostgreSQL in the source cluster and records ten successfully
  acknowledged writes in a private host-side SQLite ledger.
4. Performs a `pg_dump` logical backup.
5. Injects catastrophic failure by **deleting the source namespace** and awaiting termination.
6. Streams the backup into the PostgreSQL instance in the separate restore cluster.
7. Executes V2 connectivity checks, confirms the backed-up V3 row count,
   preserves the original V4 probe and compares all recovered IDs with the ledger.
8. Writes a `DrillRun` with acknowledged-write evidence and a zero-loss objective.
9. Automatically cleans up test namespaces in both clusters.

## 5. Evaluate the evidence report

Pass the generated evidence to `checkride-report`:

```bash
go run ./cmd/checkride-report /tmp/k3d-evidence.json
```

The evaluator will output the JSON report and exit with `0` (Verified), `1` (Failed), or `2` (Incomplete/Invalid). This previously captured V3 run is a historical example; a current successful lab run requests V4:

```json
{
  "name": "checkride-31efc5783dea",
  "scenario": "isolated-postgresql-logical-restore",
  "verdict": "verified",
  "headline": "Verified to V3",
  "requestedLevel": "V3",
  "deepestPassed": "V3"
}
```

Current runs also contain ledger evidence. With `--writes 10` and no tail, the
report should show ten acknowledged and recovered writes, zero lost writes,
RPO `0s`, and `met: true` for the `0s` objective. This was verified against
a real local k3d restore on 2026-10-05.

### Demonstrate acknowledged-write loss

Run a separate drill that inserts two more writes after taking the dump:

```bash
checkride lab run --writes 10 --after-backup-writes 2 --output /tmp/k3d-tail-loss.json
go run ./cmd/checkride-report /tmp/k3d-tail-loss.json
```

Both commands should exit `1`: the backup and V3 row count pass, but V4 fails
the zero-loss objective. The report should show twelve acknowledged writes,
ten recovered and two lost, with no holes or unexpected IDs. The measured
RPO duration depends on this run's acknowledgement and failure timestamps.
Do not treat this intentional failure as a broken backup command.

### Permit a bounded loss window explicitly

If the drill's policy allows up to 60 seconds of tail loss, declare it before
execution. Use a new output path; existing evidence and ledger files are never
overwritten:

```bash
checkride lab run --writes 10 --after-backup-writes 2 --rpo-seconds 60 \
  --output /tmp/k3d-budget-loss.json
go run ./cmd/checkride-report /tmp/k3d-budget-loss.json
```

Both commands exit `0` only if the measured RPO is within the declared budget
and all checks pass. The report still shows two lost writes; **verified does
not mean zero loss**. A budget never permits holes in the recovered prefix or
unexpected restored IDs. The runner also rejects blank, duplicate or truncated
restored-ID results that contradict its verified V3 row count; such failures
emit failed V4 evidence without a measured ledger.

Three unchanged real captures from 2026-10-05 are checked into `examples/runs/`
and available in Studio alongside the historical V3 example:

| File | Acknowledged / recovered / lost | RPO | Budget | Verdict |
| --- | --- | --- | --- | --- |
| `k3d-ledger-zero-loss.run.json` | 10 / 10 / 0 | 0s | 0s | Verified V4 |
| `k3d-ledger-tail-loss.run.json` | 12 / 10 / 2 | 0.978107s | 0s | Failed V4 |
| `k3d-ledger-budget-loss.run.json` | 12 / 10 / 2 | 0.949153s | 60s | Verified V4 |

These durations describe the captured runs, not a performance guarantee.
All three have zero holes and zero unexpected IDs. To inspect the permitted-loss
capture without creating clusters:

```bash
go run ./cmd/checkride-report examples/runs/k3d-ledger-budget-loss.run.json
```

### Ledger And Measurement Limits

`--writes` defaults to one and must be positive; `--after-backup-writes`
defaults to zero and must be nonnegative. Together they are capped at 100.
`--rpo-seconds` defaults to `0` and accepts whole seconds from `0` to `86400`.
The declared budget is included in `spec.objectives.rpo`; it is not inferred
from the observed loss after the restore.
The runner records an acknowledgement only after `psql` returns success, then
commits it to the host-side SQLite ledger. The timestamp is when the host
observed success, not the database's internal commit time.

The `<output>.ledger.db` sidecar has private file permissions and must not
already exist, just like the evidence output. Keep it outside both clusters
and retain it with the evidence. If execution stops before restored IDs are
queried and validated, the sidecar retains completed acknowledgements, but the DrillRun
omits ledger measurements rather than claiming all writes were lost. A V3
row-count failure still emits V3-only evidence.

This measures loss for a bounded, sequential PostgreSQL test workload. It is
not application-level acknowledgement instrumentation, a business invariant,
PITR/WAL validation or authenticated provenance. The bundled historical V3
sample remains unchanged.

## 6. Teardown

To delete the test clusters when done:

```bash
make lab-down
```
