---
title: Drill specification reference
description: Declarative YAML specification schema for defining Kubernetes recovery drills.
---

A `Drill` document describes the failure scenario, the target workload, the restore destination, and the required verification depth and recovery objectives.

## Full YAML example

```yaml
apiVersion: checkride/v1alpha1
kind: Drill
metadata:
  name: shop-namespace-loss
spec:
  scenario: namespace-loss
  target:
    namespace: demo-shop
    argocdApplication: demo-shop
    cnpgCluster: shop-db
  restore:
    into: separate-cluster
    pointInTime: "2026-10-01T11:59:00Z"
  verify:
    upTo: V4
    ledger: true
    invariants:
      - name: every-order-has-a-customer
        sql: "SELECT count(*) FROM orders o LEFT JOIN customers c ON c.id = o.customer_id WHERE c.id IS NULL"
        expect: 0
  objectives:
    rto: 15m
    rpo: 5m
    timeout: 30m
```

## Field reference

### `apiVersion` and `kind`
- `apiVersion`: Must be `checkride/v1alpha1`.
- `kind`: Must be `Drill`.

### `metadata`
- `metadata.name` *(string, required)*: Unique name for this drill, formatted as a DNS-1123 label (at most 63 lowercase alphanumeric characters or hyphens, starting and ending with an alphanumeric character).

### `spec.scenario` *(enum, required)*
Specifies the simulated failure mode:
- `namespace-loss`: The target application namespace is deleted.
- `cluster-loss`: Complete loss of the source Kubernetes cluster. Requires `restore.into: separate-cluster`.
- `bad-migration`: A failed database migration or application corruption requires Point-in-Time Recovery.
- `ransomware`: Application data is encrypted or corrupted; restore from immutable backup is tested.
- `lost-secret`: Decryption credentials or Secrets are lost; verifies recovery from external vault.
- `storage-class-mismatch`: Tests recovery when storage classes or CSI provisioners differ in target.

### `spec.target` *(object, required)*
Identifies the application under test:
- `namespace` *(string, required)*: The Kubernetes namespace hosting the workload.
- `argocdApplication` *(string, optional)*: The Argo CD Application resource name managing the GitOps state.
- `cnpgCluster` *(string, optional)*: CloudNativePG Cluster resource name. Required if `verify.ledger: true` or `verify.invariants` are defined.

### `spec.restore` *(object, optional)*
Defines how and where the workload is rebuilt:
- `into` *(enum, default: `separate-cluster`)*: Target environment. Either `separate-cluster` (default and recommended) or `namespace` (emits a lint warning).
- `pointInTime` *(string, optional)*: ISO 8601 timestamp with an explicit UTC offset (e.g. `2026-10-01T12:00:00Z` or `+02:00`). Timestamps without timezone offsets are rejected.

### `spec.verify` *(object, required)*
Configures post-restore verification:
- `upTo` *(string/int, default: `V4`)*: Maximum verification level (`V0` through `V4`).
- `ledger` *(bool, default: `false`)*: Enables write ledger reconciliation for exact mathematical RPO calculation.
- `invariants` *(array, optional)*: List of domain invariant assertions executed against the database:
  - `name`: Human-readable identifier.
  - `sql`: Read-only SQL query returning a scalar value.
  - `expect`: Expected scalar value (`string`, `number`, or `boolean`).

*Note:* If `upTo` is `V4`, either `ledger: true` or at least one invariant must be configured.

### `spec.objectives` *(object, optional)*
Service level objectives for recovery:
- `rto`: Recovery Time Objective formatted as a compact duration (`15m`, `1h30m`, `90s`) or seconds.
- `rpo`: Recovery Point Objective formatted as a compact duration or seconds.
- `timeout`: Maximum permitted execution time before the drill is aborted as failed.
