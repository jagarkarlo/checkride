---
title: Separate restore clusters
description: Why authentic disaster recovery testing requires physically or logically segregated target environments.
---

When disaster recovery testing is performed inside the source cluster (e.g. restoring into a temporary `restore-test` namespace), it reuses the infrastructure already running on the host. This creates a dangerous illusion of recovery readiness.

## The shared dependency trap

A production Kubernetes application depends on far more than its persistent volume data and SQL dump:

| Dependency | In-cluster restore | Segregated restore |
| --- | --- | --- |
| **CustomResourceDefinitions** | Already registered | Must be installed by GitOps |
| **Kubernetes Operators** | Running and reconciled | Must be deployed and operational |
| **StorageClasses & CSI** | Provisioners active | Provisioners must dynamically bind volumes |
| **Secrets & Keys** | Existing secrets reused | Must be decrypted/synced from external vault |
| **Network & Ingress** | Load balancers established | Ingress controllers and DNS must route |

In a real catastrophic event (regional cloud failure, cluster loss, operator corruption, or ransomware), all in-cluster state is lost. A drill that reuses those shared components will pass in testing and fail catastrophically in production.

## EU DORA Article 12(3) compliance

Regulation (EU) 2022/2554 (Digital Operational Resilience Act - DORA) mandates in Article 12(3):

> *"Financial entities shall periodically test their business continuity plans and ICT response and recovery plans [...] including restoration onto physically and logically segregated systems."*

Nostekon makes segregated restoration the **default contract** for every drill. While same-cluster namespace restoration is supported for rapid local development, the Nostekon validator emits an explicit lint warning whenever a drill targets the source cluster.

## Architecture of the k3d test lab

In the Nostekon local development lab, segregation is modeled using two isolated k3d clusters:
- `k3d-checkride-source`: Hosts the live workload, database, and scheduled backup agents.
- `k3d-checkride-restore`: An empty, independent cluster with its own control plane, etcd, network namespace, and storage volumes.

```
┌──────────────────────────────────────┐     ┌──────────────────────────────────────┐
│  Source Cluster (checkride-source)   │     │  Restore Cluster (checkride-restore) │
│                                      │     │                                      │
│  ┌────────────────┐ ┌─────────────┐  │     │  ┌────────────────┐ ┌─────────────┐  │
│  │ Source Workload│ │ CNPG / Post │  │     │  │Restored App    │ │Restored DB  │  │
│  └────────────────┘ └─────────────┘  │     │  └────────────────┘ └─────────────┘  │
│  (State simulated lost in drill)     │     │  (Verifies all dependencies rebuild) │
└──────────────────────────────────────┘     └──────────────────────────────────────┘
                   │                                            ▲
                   └────────────── Isolated Backup ─────────────┘
```

By ensuring that the restore cluster starts with no pre-existing application CRDs or Secrets, Nostekon guarantees that the restore drill proves whether the application can truly stand on its own.
