---
title: CI/CD recovery gate
description: How to gate deployments and pull requests on verified disaster recovery evidence.
---

A core tenet of Checkride is that recovery is not an annual checklist; it is a **continuous pipeline gate**. If a new database schema migration breaks Point-in-Time Recovery or an ingress change prevents pods from starting after restore, CI should fail before the code ever reaches production.

## Using `checkride-report` as a gate

The `checkride-report` binary evaluates a captured `DrillRun` evidence document and exits with machine-readable process codes:

| Exit code | Meaning | Pipeline outcome |
| --- | --- | --- |
| `0` | **Verified** | Pipeline passes. All requested levels and objectives met. |
| `1` | **Failed** | Pipeline fails. A check failed or an RTO/RPO objective was exceeded. |
| `2` | **Incomplete / Invalid** | Pipeline fails. Evidence was corrupt or required levels were not checked. |

## GitHub Actions example

### Run Checkride's isolated lab gate

The repository includes `.github/workflows/lab.yml`, named **Isolated restore
lab**. In GitHub's **Actions** tab, select that workflow and **Run workflow**
on the branch you want to test. It is manual-only and runs on an ephemeral
GitHub-hosted Ubuntu runner; it needs no production credentials or cluster
access.

The job installs k3d v5.8.3, creates the two dedicated Checkride clusters,
runs a ten-write PostgreSQL source-namespace-loss drill and evaluates its
evidence. It requires a verified V4 report with all ten writes recovered,
no holes or unexpected IDs, and RPO `0s`. A verified V3 report is not sufficient.
It then runs a deliberate post-backup tail-loss drill and requires a V4 failure
with exactly twelve acknowledged writes, ten recovered and two lost. The job
fails if this negative test unexpectedly passes or reports a different failure.
Finally it runs the same ten-plus-two workload with `--rpo-seconds 60` and
requires a verified V4 report that still reports two lost writes, a nonzero RPO
within 60 seconds, and no holes or unexpected IDs. This verifies the explicit
budget policy without treating permitted loss as zero loss.
This checks a bounded PostgreSQL workload, not general application correctness.

Download **isolated-restore-evidence-<run ID>-<attempt>** from the workflow
run's artifacts for the DrillRuns, private SQLite sidecars and JSON reports
from all three scenarios (when produced).
Artifacts are retained for seven days. On failure, inspect the failed step
and any available evidence; failures before evidence creation have no
artifact. Teardown runs with `always()` and removes the two disposable
clusters, including after partial creation or drill failure. It cannot
guarantee cleanup after an abrupt runner loss; the GitHub-hosted runner's
disposal is the final isolation boundary. The job is capped at 20 minutes
and is not supported on a persistent self-hosted runner.

All three ledger gate expressions were verified locally on 2026-10-05 using real
k3d zero-loss, deliberate two-write-tail-loss and explicit-budget runs. The
GitHub-hosted workflow itself must still be dispatched
to confirm hosted provisioning and artifact delivery.

Add this step to your deployment workflow to ensure that recent recovery evidence satisfies your application's RTO and RPO objectives:

```yaml
name: Verify Disaster Recovery

on:
  pull_request:
    paths:
      - 'migrations/**'
      - 'deploy/**'

jobs:
  recovery-gate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'

      - name: Evaluate latest drill evidence
        run: |
          go run github.com/jagarkarlo/checkride/cmd/checkride-report@v0.1.0 \
            ./evidence/latest-restore.run.json
```

If the latest restore failed or exceeded the target RTO, `checkride-report` exits with code `1`, halting the deployment pipeline.

## GitLab CI example

For teams using GitLab CI:

```yaml
stages:
  - test
  - deploy

verify-recovery:
  stage: test
  image: golang:1.25
  script:
    - go run github.com/jagarkarlo/checkride/cmd/checkride-report@v0.1.0 ./artifacts/nightly-drill.json
  rules:
    - if: '$CI_PIPELINE_SOURCE == "schedule"'
    - if: '$CI_MERGE_REQUEST_IID'
```

## Automating staleness checks

To prevent teams from passing CI with outdated evidence from months ago, you can verify the `completedAt` timestamp in the `DrillRun` metadata:

```bash
# Fail if evidence is older than 24 hours
max_age_seconds=86400
completed_epoch=$(jq -r '.status.completedAt | fromdateiso8601' evidence.json)
now_epoch=$(date +%s)

if [ $(( now_epoch - completed_epoch )) -gt $max_age_seconds ]; then
  echo "Error: Recovery evidence is older than 24 hours. Run a fresh drill."
  exit 1
fi

checkride-report evidence.json
```

## Watching results over time

A pass/fail gate tells you about the latest run. To see recovery time and data loss trend across many runs, push the same evidence to Prometheus and chart it in Grafana; see [Recovery metrics and the Grafana dashboard](metrics-and-dashboard.md).

