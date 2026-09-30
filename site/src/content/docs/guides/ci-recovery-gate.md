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
