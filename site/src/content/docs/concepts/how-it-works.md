---
title: How Nostekon works
description: The end-to-end lifecycle of an automated Kubernetes recovery drill.
---

Most disaster recovery solutions stop after taking a snapshot and verifying that the backup job returned an exit code of zero. Nostekon tests the opposite end of the pipeline: **restoration and data correctness**.

## The core recovery loop

Nostekon structures every recovery test as an isolated, verifiable loop:

```
[ Drill Specification ]
          │
          ▼
┌──────────────────┐      Writes & Acks      ┌───────────────────┐
│  Source Cluster  │ ──────────────────────> │  External Ledger  │
└──────────────────┘                         └───────────────────┘
          │ (Periodic Backups)                         │
          ▼                                            │
┌──────────────────┐                                   │
│  Backup Storage  │                                   │
│ (CloudNativePG/  │                                   │
│     Velero)      │                                   │
└──────────────────┘                                   │
          │ (PITR / Restore)                           │
          ▼                                            │
┌──────────────────┐                                   │
│ Separate Restore │                                   │
│     Cluster      │ <─────────────────────────────────┘
└──────────────────┘          V0–V4 Checks & Exact RPO
          │
          ▼
┌──────────────────┐
│ Evidence Report  │ ──> CI Recovery Gate / Audit Artifact
└──────────────────┘
```

### 1. Declarative drill specification
You author a declarative `Drill` document (`checkride/v1alpha1`) specifying:
- **Scenario:** The failure model (`namespace-loss`, `cluster-loss`, `bad-migration`, `ransomware`, `lost-secret`, etc.).
- **Target:** The workload namespace, GitOps Argo CD application name, and database cluster.
- **Restore target:** Destination environment, defaulting to an isolated separate cluster.
- **Verification depth:** The maximum verification level to evaluate (`V0` to `V4`).
- **Objectives:** Maximum tolerable recovery time (RTO) and data loss (RPO).

### 2. Acknowledged-write tracking
To calculate exact Recovery Point Objective (RPO), the application records write acknowledgements into an append-only ledger stored **outside** the cluster under test. When failure strikes, the ledger represents the objective ground truth of what users were promised had been committed.

### 3. Segregated restoration
Rather than restoring into a test namespace inside the same cluster—which reuses existing Operators, CustomResourceDefinitions (CRDs), storage classes, and shared Secrets—Nostekon restores onto a physically or logically segregated cluster. This directly satisfies regulatory requirements such as **EU DORA Article 12(3)**.

### 4. Hierarchical V0–V4 verification
Once pods schedule and database engines start, Nostekon executes verification in strictly ordered levels:
- **V0:** Did the backup tool confirm completion?
- **V1:** Did the restore tool report successful completion?
- **V2:** Are pods running and passing HTTP/TCP health probes?
- **V3:** Are database schemas, table row counts, and checksums intact?
- **V4:** Do business invariants hold, and does the restored state match acknowledged writes?

### 5. Audit-grade evidence reporting
The output of every run is a versioned `DrillRun` evidence document. The Nostekon verifier parses this evidence to compute:
- The deepest contiguous verification level passed.
- The earliest failing check.
- Wall-clock Recovery Time Objective (RTO) breakdown with uncovered gaps.
- Exact Recovery Point Objective (RPO) and ledger timeline.
