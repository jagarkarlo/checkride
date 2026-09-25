---
title: Verification levels
description: Know exactly which evidence a recovery check establishes.
---

Each level asks a different question. Passing a lower level does not imply passing the next one.

| Level | Question | Example evidence |
| --- | --- | --- |
| V0 | Did the backup report success? | Backup tool status |
| V1 | Did the restore report success? | Restore job status |
| V2 | Is the workload healthy? | Ready pods, HTTP and TCP checks |
| V3 | Is the data structurally intact? | Tables, row counts, checksums |
| V4 | Is the data correct? | Business invariants and acknowledged-write ledger |

The intended report records the deepest level passed and the first level that failed. Today the validator checks that a requested level has the fields and evidence requirements expected by the drill specification. Automated restore execution and end-to-end reports are planned, not shipped.