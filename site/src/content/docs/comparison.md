---
title: Comparison
description: Where Nostekon fits alongside backup and restore tools.
---

Nostekon is intended to **exercise** recovery, not replace a backup engine. These are differences in scope, not claims about a vendor's quality.

| Approach | Primary job | Typical evidence | What remains to prove |
| --- | --- | --- | --- |
| Backup and restore tools, such as CloudNativePG and Velero | Capture and recover database or Kubernetes state | Backup and restore job status | Application health and correctness of recovered data |
| Sandbox restore or health checks | Restore into an isolated environment and probe the workload | Readiness and basic functional checks | Whether acknowledged writes and business invariants survived |
| Nostekon (planned) | Orchestrate isolated drills and compare recovery evidence | Verification levels V0-V4, RTO and exact RPO | Scope depends on the scenario and checks you define |

**Current state:** Nostekon ships the drill validator and ledger foundation, not a replacement for any of the tools above. Do not use it as a sole recovery control. The open-source plan is to integrate existing backup systems and make the quality of the restore measurable.