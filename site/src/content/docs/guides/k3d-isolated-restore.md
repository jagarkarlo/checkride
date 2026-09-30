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
- **Go:** 1.22+ (for `checkride-report`)

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
checkride lab run --output /tmp/k3d-evidence.json
```

What this does:
1. Validates that `k3d-checkride-source` and `k3d-checkride-restore` address distinct Kubernetes control planes.
2. Creates an ephemeral `checkride-<id>` namespace in both clusters.
3. Launches a PostgreSQL pod in the source cluster and seeds a unique write probe.
4. Performs a `pg_dump` logical backup.
5. Injects catastrophic failure by **deleting the source namespace** and awaiting termination.
6. Streams the backup into the PostgreSQL instance in the separate restore cluster.
7. Executes V2 connectivity checks and V3 row-level data comparison.
8. Writes an audit-ready `DrillRun` document to the output path.
9. Automatically cleans up test namespaces in both clusters.

## 5. Evaluate the evidence report

Pass the generated evidence to `checkride-report`:

```bash
go run ./cmd/checkride-report /tmp/k3d-evidence.json
```

The evaluator will output the JSON report and exit with `0` (Verified), `1` (Failed), or `2` (Incomplete/Invalid):

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

## 6. Teardown

To delete the test clusters when done:

```bash
make lab-down
```
