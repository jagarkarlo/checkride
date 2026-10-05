---
title: Project overview
description: Install Nostekon, run an isolated restore and inspect the evidence.
---

# Nostekon Documentation

Nostekon tests whether an application and its data can recover, not just whether a backup exists. Start with a disposable PostgreSQL restore across two separate k3d clusters, then inspect the recorded evidence in Studio. The CLI, Python package and evidence format still use the name `checkride`.

!!! warning "Pre-alpha: disposable labs only"
    The runner creates and deletes its own namespaces, including the source after backup. Do not use shared or production clusters. Reports evaluate supplied observations; they do not authenticate evidence or certify compliance.

## Start Here

| Your goal | Next step |
| --- | --- |
| Try the report evaluator without installing anything | [Open the full Studio demo](/demo/index.html) |
| Install the CLI and local Studio | [Installation](start.md) |
| Execute a real source-loss and restore drill | [First restore drill](guides/k3d-isolated-restore.md) |
| Reject a failed or incomplete recovery in CI | [Recovery gate](guides/ci-recovery-gate.md) |

## What You Can Run Today

1. Validate a Drill specification using the Python CLI, Go API or Studio.
2. Run the isolated PostgreSQL lab with `checkride lab run`.
3. Import its DrillRun JSON into Studio to inspect the verdict, checks and recovery phases.
4. Export JSON or Markdown reports, or evaluate evidence using the `checkride-report` CLI.

The browser demo runs the same Go evaluator in WebAssembly. It includes a recorded local lab run and clearly labeled synthetic examples. It does not contact Kubernetes or upload your evidence to a server.

## Understand a Result

Read [verification levels](levels.md) before treating a green result as a recovery guarantee. V0 through V4 answer different questions, and an unmeasured objective is not a passed objective. The [write ledger guide](concepts/write-ledger-rpo.md) explains what acknowledged-write evidence can establish about data loss.

The current runner observes V0 through V3 for one PostgreSQL logical restore. Scheduled drills, production backup adapters, workload-specific checks and trusted provenance remain on the [roadmap](/roadmap/).

## Project Links

[Product](/) · [System design](/product/) · [Evidence model](/evidence/) · [GitHub](https://github.com/jagarkarlo/checkride)

Source access currently requires permission because the repository is private.