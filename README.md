# Checkride

**Don't assume you can recover. Prove it, every day.**

[![CI](https://github.com/jagarkarlo/checkride/actions/workflows/ci.yml/badge.svg)](https://github.com/jagarkarlo/checkride/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Checkride runs automated restore drills for Kubernetes applications backed by
PostgreSQL. It restores the whole application (GitOps state, CloudNativePG
point-in-time recovery, volumes, Secrets and object storage) into a separate
cluster, then verifies that the *right data* came back. It measures how long
every recovery phase took and exactly how much acknowledged data was lost.

> A checkride is the practical exam where a pilot has to fly the manoeuvres.
> Saying you could is not enough.

> [!NOTE]
> **Status: pre-alpha.** Checkride is being built as a master's thesis project
> (2026-2027). Interfaces will change without notice.

## Why

Backup tools report `Completed`. That proves a backup was written, not that
your application can be restored from it.

- CloudNativePG documents an RPO of at most five minutes with WAL archiving and
  asks users to "regularly test restoring from your backups" and "measure the
  time required for a full recovery".
- EU DORA Article 12 requires periodic restore testing, restoration onto
  segregated systems, and reconciliation checks so data stays consistent
  between systems.
- Most restore tests stop at "the pods are healthy" or "the tables have rows".
  Neither proves that acknowledged writes survived.

## Verification levels

| Level | Question | Evidence |
|---|---|---|
| V0 | Did the backup report success? | Backup tool status |
| V1 | Did the restore report success? | Restore tool status |
| V2 | Is the workload healthy? | Pods ready, HTTP and TCP checks |
| V3 | Is the data structurally intact? | Tables, row counts, checksums |
| V4 | Is the data correct? | Business invariants, acknowledged-write ledger, cross-store consistency |

Every drill reports the deepest level it passed and the first level that
caught a failure.

## How it works

```mermaid
flowchart LR
    Spec[Drill spec] --> Orchestrator
    Orchestrator --> Source[Source cluster]
    Source -- acknowledged writes --> Ledger[(Write ledger)]
    Source --> Backup[CloudNativePG + Velero backups]
    Orchestrator --> Fault[Fault injection]
    Backup --> Restore[Separate restore cluster]
    Restore --> Verifier[Verifier V0-V4]
    Ledger --> Verifier
    Verifier --> Report[RTO phases, exact RPO, evidence]
    Report --> Studio[Studio UI]
    Report --> Gate[CI gate]
```

## Components

| Component | Purpose | State |
|---|---|---|
| Drill spec | Declarative YAML description of a drill | Validation in Python, Go API and Studio |
| Ledger | Records acknowledged writes outside the cluster and computes exact RPO | Python ledger; Go report evaluation |
| DrillRun | Bounded JSON evidence document with a published JSON Schema | Available; provenance not authenticated |
| Verifier | Evaluates recorded V0-V4 checks, RTO phases and RPO ledger | Available for submitted evidence; no restore execution |
| API | Go HTTP control plane, validation, schema and report endpoints | Available locally |
| Report CLI | Evaluates DrillRun files and returns gate-friendly exit codes | Available as `go run ./cmd/checkride-report` |
| Attestation CLI | Signs exact DrillRun bytes and verifies detached signatures against trusted keys | Available as `go run ./cmd/checkride-attest` |
| Metrics | Pushes per-drill gauges to a Prometheus Pushgateway, with a bundled Grafana dashboard | Available via `checkride-report --pushgateway-url` |
| Lab | Disposable k3d source and restore clusters | Started |
| Orchestrator | Runs drills, times every phase, cleans up | Planned |
| Analyzer | Predicts restore failures before a drill from manifests and configuration | Planned |
| Studio | Drill specification workbench and evidence report UI with JSON/Markdown export | Available locally |
| Copilot | LLM that diagnoses failed drills and proposes fixes that must pass a re-run | Planned |
| Gate | CI check that fails when the last verified restore is too old or failed | Planned |

## Quick start (development)

```bash
git clone https://github.com/jagarkarlo/checkride.git
cd checkride
python3 -m venv .venv && . .venv/bin/activate
pip install -e ".[dev]"

checkride levels
checkride validate examples/drills/*.yaml
pytest
```

The Go API is in early development. From the repository root:

```bash
go test -race ./...
go run ./cmd/checkride-api
```

It listens on `:8080` by default. Set `CHECKRIDE_ADDR` to change the address;
`GET /healthz` and `GET /readyz` return `204 No Content` while the process is
healthy. Readiness will become dependency-aware as the API gains external
services.

The first Studio-facing endpoint validates a JSON drill document:

```bash
curl -sS http://localhost:8080/api/v1/drills/validate \
  -H 'Content-Type: application/json' \
  --data-binary @drill.json
```

It returns `200` with `valid`, `errors` and `warnings` for valid documents,
`422` for well-formed but invalid drills, `400` for malformed or unknown JSON,
`413` for bodies over 1 MiB, and `415` for other media types. The API currently
validates the core drill contract; it does not create or execute a drill.

Recorded evidence can be evaluated through `POST /api/v1/runs/report` or the
standalone Go command:

```bash
go run ./cmd/checkride-report examples/runs/mlflow-namespace-loss.run.json
```

The API accepts up to 16 MiB and evaluates at most four reports concurrently.
`GET /api/v1/schemas/drillrun` returns the versioned JSON Schema. The command
prints a JSON report and exits `0` for verified, `1` for failed, and `2` for
incomplete or invalid evidence. The original MLflow and CRUD example runs are synthetic; the k3d PostgreSQL example is a locally captured lab run. The evaluator
checks submitted claims; it does not execute restores or authenticate who
recorded the evidence. `V4` requires a write ledger or a declared invariant
with a matching check; ledger loss must also meet any declared RPO objective.

### Sign and verify evidence

An operator can create a detached Ed25519 signature over the exact DrillRun
file bytes. The public key must be distributed and trusted independently:

```bash
install -d -m 700 "$HOME/.config/checkride"
go run ./cmd/checkride-attest keygen --private "$HOME/.config/checkride/signing-key.pem" --public "$HOME/.config/checkride/trusted-key.pem"
go run ./cmd/checkride-attest sign --evidence /tmp/checkride-drill.json --key "$HOME/.config/checkride/signing-key.pem" --output /tmp/checkride-drill.attestation.json
go run ./cmd/checkride-attest verify --evidence /tmp/checkride-drill.json --attestation /tmp/checkride-drill.attestation.json --trusted-key "$HOME/.config/checkride/trusted-key.pem"
go run ./cmd/checkride-report --attestation /tmp/checkride-drill.attestation.json --trusted-key "$HOME/.config/checkride/trusted-key.pem" /tmp/checkride-drill.json
```

Keep the private key outside the repository and distribute the public key
through a trusted channel. Key generation refuses to overwrite files, and
signing refuses private keys accessible to group or other users on Unix-like
systems. On Windows, restrict the key with filesystem ACLs. Verification
checks the evidence-byte digest, key fingerprint and signature. This proves
that the exact file was signed by the holder of the trusted key; it does not
prove that the runner was truthful, identify a person without an independently
maintained key-to-identity mapping, or establish when the signature was made.
By default, `checkride-report` marks evidence as `unverified`; with both
`--attestation` and `--trusted-key`, it verifies the signature and includes the
provenance status, key ID and evidence digest in its JSON report. Invalid
attestations fail report generation. The API does not automatically verify
sidecars or trust caller-supplied keys.

### Push recovery metrics to Grafana

`checkride-report` can push one gauge per drill to a Prometheus Pushgateway,
so results land in the same dashboards as the rest of your stack instead of
only a CI log:

```bash
go run ./cmd/checkride-report --pushgateway-url http://pushgateway:9091 /tmp/checkride-drill.json
```

The instance label defaults to the evidence's `metadata.name`; set
`--pushgateway-instance` explicitly in a scheduled job so every run keeps its
own series instead of overwriting the last one. A push failure is printed as
a warning and does not change the exit code. Import
[`grafana/checkride-recovery-dashboard.json`](grafana/checkride-recovery-dashboard.json)
for a ready dashboard, or read the
[full guide](site/src/content/docs/guides/metrics-and-dashboard.md).

To use the Studio locally, start the API and Studio in separate terminals:

```bash
go run ./cmd/checkride-api
cd studio && npm ci && npm run dev
```

Open `http://127.0.0.1:5173`. The development server proxies `/api` and
`/healthz` requests to the Go API on port 8080. The **Design drill** view
starts from a scenario template or imported JSON; the plan and V0-V4 depth
update as you edit. Syntax errors show their line, and selecting a validation
problem jumps to its field. `Ctrl+Enter` validates. The **Evidence report**
view imports a DrillRun, plots recovery phases and ledger outcomes, and exports
JSON or Markdown. Run Studio tests with `npm test`.

To run the disposable isolated PostgreSQL source-loss drill, install Docker,
k3d, kubectl, Python 3.12+ and Go, then use only the dedicated lab contexts:

```bash
make lab-up
python3 scripts/lab/restore.py --output /tmp/checkride-drill.json
go run ./cmd/checkride-report /tmp/checkride-drill.json
make lab-down
```

The runner checks that the two clusters differ, dumps a seeded write, deletes
the source namespace, restores in the other cluster and verifies the write. It
records V0–V3, not V4, RPO or an authenticated provenance claim. It attempts
cleanup even on failure; check for leftover `checkride-*` namespaces if cleanup
reports an error. The [lab walkthrough](site/src/content/docs/start.md) covers
prerequisites and proxy-restricted image pulls. This is not production-safe
orchestration.

The Astro product site connects the full browser Studio with MkDocs Material
documentation under `/docs/`. Install Python 3.12+, Node 22+ and Go, then build
the demo and documentation before starting the site:

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -e ".[docs]"
npm ci --prefix studio
npm ci --prefix site
npm run build --prefix site
npm run dev --prefix site
```

Open `http://127.0.0.1:4321`. Rebuild docs after Markdown changes with
`bash scripts/build-docs.sh`; `mkdocs build --strict` checks navigation and
internal Markdown links. The demo at `/demo/` evaluates reports in the browser;
the standalone Studio can also connect to the local Go API. Checkride is
pre-alpha and only executes the narrow disposable lab drill. A paid enterprise
edition is not planned until the open-source restore workflow works reliably
end to end and operators validate a concrete need for supported deployments,
policy controls, or fleet-wide reporting.

## Related projects

Checkride builds on these tools and is evaluated against them:

- [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg) with the
  [Barman Cloud plugin](https://github.com/cloudnative-pg/plugin-barman-cloud)
  and [Velero](https://github.com/velero-io/velero) create the backups that
  Checkride restores.
- [Kymaros](https://github.com/kymaroshq/kymaros) restores Velero backups into
  sandbox namespaces and checks workload health (V2).
- [Databasus](https://github.com/databasus/databasus) restores PostgreSQL
  backups into a container and reports row counts per table (V3).
- [KubeStash](https://github.com/kubestash) offers a `BackupVerifier` that
  restores and runs queries for KubeDB-managed databases.
- [AWS Backup restore testing](https://docs.aws.amazon.com/aws-backup/latest/devguide/restore-testing.html)
  schedules test restores for AWS services; Kubernetes is not covered.

## Roadmap

| Milestone | Exit criteria |
|---|---|
| M0 Pilot |  Timed namespace-loss and cluster-loss drills on k3d |
| M1 Harness | Five scenarios automated; at least 30 unattended drills per night, 90% valid |
| M2 Failure study |  Failure classes from real incidents reproduced and measured at V0-V4 |
| M3 Analyzer |  Pre-drill failure prediction evaluated against baselines |
| M4 Copilot |  Drill-verified LLM repair evaluated |
| M5 Studio and Gate |  Web UI and CI gate usable end to end |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as
described in [SECURITY.md](SECURITY.md).

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
