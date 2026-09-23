# 2. Restore into a separate cluster by default

- Status: accepted

## Context

A restore into a sandbox namespace of the source cluster reuses its CRDs,
operators, storage classes and Secrets. Failures caused by those missing
dependencies, which are common after a real cluster loss, stay hidden. EU DORA
Article 12(3) also asks for restoration onto physically and logically
segregated systems.

## Decision

Drills restore into a separate cluster by default. Namespace restores remain
possible, but the validator warns about them. A cluster-loss drill must
restore into a separate cluster. In the lab, the source and restore clusters
are two k3d clusters with their own API servers and networks.

## Consequences

- Drills take longer, because the restore cluster has to be bootstrapped.
- Missing CRDs, operators and Secrets surface as real drill failures.
- vCluster is not the default target, because Velero file-system backup does
  not support it.
