---
title: Recovery metrics and the Grafana dashboard
description: Push recovery drill results to a Prometheus Pushgateway and chart them with the bundled Grafana dashboard.
---

`nostekon-report` can push one gauge per drill to a [Prometheus Pushgateway](https://github.com/prometheus/pushgateway), so recovery results show up next to the rest of your observability stack instead of living only in a CI log.

## Why a Pushgateway

The lab runner and `nostekon-report` are short-lived commands. Prometheus scrapes long-running services; it cannot scrape a process that has already exited. The Pushgateway sits in between: your job pushes its result once, and Prometheus scrapes the Pushgateway on its normal schedule.

The Pushgateway keeps only the **last** value pushed for a given job and instance label. To build a history instead of overwriting one point, give every run a distinct `instance` label, for example a run ID or a timestamp. `nostekon-report` defaults the instance label to the DrillRun's `metadata.name`, which already includes a date in the bundled examples.

## Pushing metrics

```bash
go run ./cmd/nostekon-report \
  --pushgateway-url http://pushgateway.monitoring.svc:9091 \
  --pushgateway-job nostekon \
  /tmp/drill.json
```

- `--pushgateway-job` defaults to `nostekon`.
- `--pushgateway-instance` defaults to the evidence's `metadata.name`; set it explicitly in a scheduled job so every run gets its own series.
- A push failure is printed as a warning and does not change the exit code. Metrics are observability, not part of the pass/fail gate described in [CI recovery gate](ci-recovery-gate.md).

## What gets pushed

| Metric | Meaning |
| --- | --- |
| `nostekon_drill_verified` | `1` if the verdict is verified, `0` otherwise |
| `nostekon_drill_requested_level` | Ordinal of the requested depth (`V0`=0 .. `V4`=4) |
| `nostekon_drill_deepest_level` | Ordinal of the deepest contiguous level passed, or `-1` if none |
| `nostekon_recovery_time_seconds` | Measured RTO, or `-1` if unmeasured |
| `nostekon_recovery_time_met` | `1` met, `0` missed, `-1` unmeasured or no objective |
| `nostekon_data_loss_seconds` | Measured RPO, or `-1` if unmeasured |
| `nostekon_data_loss_met` | `1` met, `0` missed, `-1` unmeasured or no objective |
| `nostekon_acknowledged_writes_total` | Writes in the ledger before the failure, or `-1` |
| `nostekon_acknowledged_writes_lost` | Writes not found after recovery, or `-1` |
| `nostekon_evidence_verified` | `1` if the report verified a detached signature, `0` otherwise |

A value of `-1` means the field was not measured, which is different from `0`. Build your Grafana queries and alerts with that in mind, for example `nostekon_recovery_time_seconds >= 0` before averaging.

Metrics were named `checkride_*` before the Nostekon rename; update existing dashboards and alert rules to the new names.

## Importing the dashboard

The repository ships [`grafana/nostekon-recovery-dashboard.json`](https://github.com/jagarkarlo/nostekon/blob/main/grafana/nostekon-recovery-dashboard.json): a stat row for the latest verdict, depth, signature status and writes lost, plus time series for recovery time and data loss and a table of the latest run per instance.

1. In Grafana, go to **Dashboards → New → Import**.
2. Upload `nostekon-recovery-dashboard.json` or paste its contents.
3. Select the Prometheus datasource that scrapes your Pushgateway.
4. Use the `job` and `instance` dropdowns at the top to narrow the view to one drill or compare several.

## Pruning old Pushgateway groups

Because the Pushgateway keeps every group you have ever pushed until it is deleted or the Pushgateway restarts, a unique `instance` label per run means the group list grows without bound. Once Prometheus has scraped a result you no longer need, delete it:

```bash
curl -X DELETE http://pushgateway.monitoring.svc:9091/metrics/job/nostekon/instance/<run-name>
```

Run this on a schedule, or keep only as much history as your Prometheus retention needs by deleting groups older than a few scrape intervals.
