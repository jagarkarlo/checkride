---
title: k3d isolated cluster runbook
description: Step-by-step guide to executing a real PostgreSQL disaster recovery drill across two local k3d clusters.
---

This runbook guides you through provisioning two segregated Kubernetes clusters on your workstation using k3d, executing a PostgreSQL logical backup and restore drill, and verifying the evidence.

## Prerequisites

Ensure the following tools are installed:
- **Docker:** 24.0+ (engine running)
- **k3d:** v5.4+ (`k3d version`)
- **kubectl:** v1.28+
- **Python:** 3.12+ with Nostekon installed (`pip install -e .`)
- **Go:** 1.25+ (for `nostekon-report`)

## 1. Create the segregated clusters

Nostekon bundles declarative cluster definitions in `lab/k3d/source.yaml` and `lab/k3d/restore.yaml`. Run:

```bash
make lab-up
```

Or manually:

```bash
k3d cluster create --config lab/k3d/source.yaml
k3d cluster create --config lab/k3d/restore.yaml
```

This creates:
- `k3d-nostekon-source`: Source cluster on dedicated Docker bridge network.
- `k3d-nostekon-restore`: Target cluster with isolated API server, network, and storage.

## 2. Proxy and offline image caching (if needed)

If running in corporate or restricted network environments where nodes cannot pull from Docker Hub, import the required images from the host daemon into both clusters:

```bash
docker pull rancher/mirrored-pause:3.10.2
docker pull postgres:16.8

k3d image import -c nostekon-source -c nostekon-restore \
  rancher/mirrored-pause:3.10.2 \
  postgres:16.8
```

## 3. Verify cluster readiness

Check that both clusters are reachable and have ready nodes:

```bash
nostekon lab status
```

Expected output:
```
source   [READY    ] context=k3d-nostekon-source nodes=2
restore  [READY    ] context=k3d-nostekon-restore nodes=2
```

## 4. Execute the recovery drill

Run the automated PostgreSQL disaster recovery drill:

```bash
nostekon lab run --writes 10 --output /tmp/k3d-evidence.json
```

What this does:
1. Validates that `k3d-nostekon-source` and `k3d-nostekon-restore` address distinct Kubernetes control planes.
2. Creates an ephemeral `nostekon-<id>` namespace in both clusters.
3. Launches PostgreSQL in the source cluster and records ten successfully
  acknowledged writes in a private host-side SQLite ledger.
4. Performs a `pg_dump` logical backup.
5. Injects catastrophic failure by **deleting the source namespace** and awaiting termination.
6. Streams the backup into the PostgreSQL instance in the separate restore cluster.
7. Executes V2 connectivity checks, confirms the backed-up V3 row count,
   preserves the original V4 probe and compares all recovered IDs with the ledger.
8. Writes a `DrillRun` with acknowledged-write evidence and a zero-loss objective.
9. Automatically cleans up test namespaces in both clusters.

## 5. Evaluate the evidence report

Pass the generated evidence to `nostekon-report`:

```bash
go run ./cmd/nostekon-report /tmp/k3d-evidence.json
```

The evaluator will output the JSON report and exit with `0` (Verified), `1` (Failed), or `2` (Incomplete/Invalid). This previously captured V3 run is a historical example; a current successful lab run requests V4:

```json
{
  "name": "checkride-31efc5783dea",
  "scenario": "isolated-postgresql-logical-restore",
  "verdict": "verified",
  "headline": "Verified to V3",
  "requestedLevel": "V3",
  "deepestPassed": "V3"
}
```

Current runs also contain ledger evidence. With `--writes 10` and no tail, the
report should show ten acknowledged and recovered writes, zero lost writes,
RPO `0s`, and `met: true` for the `0s` objective. This was verified against
a real local k3d restore on 2026-10-05.

### Demonstrate acknowledged-write loss

Run a separate drill that inserts two more writes after taking the dump:

```bash
nostekon lab run --writes 10 --after-backup-writes 2 --output /tmp/k3d-tail-loss.json
go run ./cmd/nostekon-report /tmp/k3d-tail-loss.json
```

Both commands should exit `1`: the backup and V3 row count pass, but V4 fails
the zero-loss objective. The report should show twelve acknowledged writes,
ten recovered and two lost, with no holes or unexpected IDs. The measured
RPO duration depends on this run's acknowledgement and failure timestamps.
Do not treat this intentional failure as a broken backup command.

### Permit a bounded loss window explicitly

If the drill's policy allows up to 60 seconds of tail loss, declare it before
execution. Use a new output path; existing evidence and ledger files are never
overwritten:

```bash
nostekon lab run --writes 10 --after-backup-writes 2 --rpo-seconds 60 \
  --output /tmp/k3d-budget-loss.json
go run ./cmd/nostekon-report /tmp/k3d-budget-loss.json
```

Both commands exit `0` only if the measured RPO is within the declared budget
and all checks pass. The report still shows two lost writes; **verified does
not mean zero loss**. A budget never permits holes in the recovered prefix or
unexpected restored IDs. The runner also rejects blank, duplicate or truncated
restored-ID results that contradict its verified V3 row count; such failures
emit failed V4 evidence without a measured ledger.

Three unchanged real captures from 2026-10-05 are checked into `examples/runs/`
and available in Studio alongside the historical V3 example:

| File | Acknowledged / recovered / lost | RPO | Budget | Verdict |
| --- | --- | --- | --- | --- |
| `k3d-ledger-zero-loss.run.json` | 10 / 10 / 0 | 0s | 0s | Verified V4 |
| `k3d-ledger-tail-loss.run.json` | 12 / 10 / 2 | 0.978107s | 0s | Failed V4 |
| `k3d-ledger-budget-loss.run.json` | 12 / 10 / 2 | 0.949153s | 60s | Verified V4 |

These durations describe the captured runs, not a performance guarantee.
All three have zero holes and zero unexpected IDs. To inspect the permitted-loss
capture without creating clusters:

```bash
go run ./cmd/nostekon-report examples/runs/k3d-ledger-budget-loss.run.json
```

### Run the three-case policy suite

Run all three outcomes as one regression gate against the same two disposable
clusters. Choose an output directory that does not already exist:

```bash
nostekon lab suite --output-dir /tmp/nostekon-suite-001
```

The suite defaults to ten writes before each backup and runs these cases in order:

| Case | Writes after backup | RPO objective | Expected drill exit |
| --- | --- | --- | --- |
| `zero-loss` | 0 | 0s | 0 |
| `tail-loss` | 2 | 0s | 1 |
| `budget-loss` | 2 | 60s | 0 |

The **suite exits 0 when all three measured outcomes match their expectations**.
The strict `tail-loss` DrillRun remains failed; it is not rewritten as verified.
The suite checks the lost-write counts, holes, unexpected IDs, objectives and
V0-V4 evidence, including the original probe. An unrelated restore, query or
cleanup error does not count as the expected strict-policy failure. An unexpected
outcome stops the suite and returns 1 without executing later cases.

Each case produces `<case>.drillrun.json` and its SQLite ledger. The directory
uses mode `0700`; the summary, evidence and ledger files use `0600`. The suite
rejects an existing output directory before accessing clusters. It persists
`suite.json` before each case and after evaluation, including expected and
observed exits, measured RPO and the final suite status. Keep individual evidence
files for independent evaluation; the summary is a regression result, not a
replacement for a DrillRun report or authenticated provenance.

Each checkpoint is serialized into a private `0600` temporary file in the
suite directory. The runner flushes and syncs that file, atomically replaces
`suite.json`, then syncs the directory. A serialization, file-sync or replacement
failure leaves the previous published snapshot unchanged. A directory-sync
failure occurs after replacement: the new JSON is complete, but durability is
unconfirmed and execution stops with an error.

Once a snapshot has been published, interruption during a later write does not
replace it with partial JSON. Before the first publication, there may be no
complete summary. Atomic publication does not make all suite artifacts one
transaction or automatically resume an interrupted run.

To evaluate the intentionally failed case independently, build the Go binary
so its exit status is preserved:

```bash
go build -o /tmp/nostekon-report ./cmd/nostekon-report
/tmp/nostekon-report /tmp/nostekon-suite-001/tail-loss.drillrun.json
```

This evaluator should return 1 and still report the measured two lost writes.
Studio comparisons display both recovery objectives and warn when policies
differ, so a budget change is not mistaken for an improved recovery.

Suite `--writes` accepts 1-98; the two tail writes keep the total at most 100.
Suite `--rpo-seconds` accepts 1-86400 and changes only the `budget-loss` objective
(default 60). The other two cases retain `0s`. Optional `--source-context`,
`--restore-context` and `--image` use the same isolation checks as `lab run`.
SIGINT or SIGTERM returns 130 and attempts normal cleanup. Inspect the summary
and cleanup messages before rerunning with a fresh directory. A forcibly killed
process can leave the last complete summary marked **running**, with fewer cases
than the available evidence, and namespaces requiring cleanup. It can also leave
private `.suite-*.tmp` files. Do not promote those files into `suite.json`; retain
the published snapshot and inspect the individual evidence and ledger files.
The suite does not automatically recover or clean up after an uncatchable stop.

Cancellation during cleanup takes precedence over an earlier execution error.
The runner still attempts cleanup for both created namespaces and records both
the original failure and cleanup cancellation in the DrillRun checks. Cleanup
is best-effort: an attempted deletion does not prove that the namespace was
removed. Check any cleanup failures before starting another suite.

Cancellation during case execution records `observedExitCode: 130`. If the
drill has already finished and cancellation occurs during policy evaluation,
its original observed exit (`0` or `1`) is preserved instead. In both cases,
the summary records `status: interrupted`, `passed: false` and a completion
timestamp. The unfinished case records `error: lab interrupted`; earlier
completed case results and artifacts are retained, and no later case starts.
Repeated cancellation during final evidence/checkpoint writing is not covered
by the final-status guarantee. The published summary remains a complete snapshot,
but other artifacts may be partial and the latest progress may not be published.

Verified on 2026-10-05 with three real two-cluster restores: drill exits 0/1/0,
lost writes 0/2/2, every measured RPO field matching the independent Go evaluator,
private artifact permissions and all six temporary namespaces removed.
The cancellation precedence and evaluation checkpoints above were verified
separately with command-boundary tests on 2026-10-05, including interruption
during each of the three cases. Those tests did not run against live clusters.
Atomic checkpoint publication was verified on 2026-10-07 with serialization,
file-sync, replacement and directory-sync failure tests, plus a bounded child
process killed with SIGKILL while writing its next snapshot. The preceding
published summary was retained byte-for-byte; no live clusters were involved.

### Manual CI suite gate

The **Isolated restore lab** GitHub Actions workflow is manual-only. A repository
operator can select **Run workflow** for an approved revision; ordinary pushes
do not start Kubernetes lab clusters. The job uses a disposable Ubuntu runner,
creates the two dedicated k3d clusters and runs:

```bash
nostekon lab suite --writes 10 --rpo-seconds 60 --output-dir lab-results/suite
```

The suite must exit `0` with its expected case exits `0/1/0`. The workflow then
evaluates all three original DrillRuns independently with the Go report binary,
retaining the zero-loss, strict-loss and budgeted-loss verdict checks. Its final
gate requires a completed, passed summary with the three ordered cases and
compares all eight RPO fields against those Go reports. Loss-window seconds
allow a difference of at most `0.000001`; counts, budget and `met` must agree
exactly. A missing case, unexpected exit or changed summary claim fails the job.

The retained artifact contains the summary, original case evidence and SQLite
ledgers under `lab-results/suite/`, plus the three independent reports under
`lab-results/`. Cleanup and artifact upload are attempted even after a failed
step. Retention remains seven days; interrupted jobs are not automatically
resumed, and cleanup is still best-effort.

Verified locally on 2026-10-07: workflow lint, all three Go report shell steps
against the unchanged recorded bundle, and summary-gate tests for every RPO
field, allowed timing tolerance, case order, exits and incomplete results.
Those checks do not establish that this revised manual lab job has run on
GitHub; a successful ordinary CI run is not a live-cluster lab result.

### Run the suite from Studio

This is an opt-in Linux host feature, not remote cluster management. Prepare the
two dedicated clusters and PostgreSQL image using this guide first. The host
needs the editable Python installation (`.[dev]`), `kubectl`, the lab kubeconfig,
Go 1.25 and a built Studio. Jobs always use the CLI's fixed local
`k3d-nostekon-source` and `k3d-nostekon-restore` contexts; the browser cannot choose
an executable, command, image, cluster context or filesystem path.

From the repository root, after installing the Python package and preparing the
lab:

```bash
npm ci --prefix studio
npm run build --prefix studio
NOSTEKON_ADDR=127.0.0.1:8181 \
  NOSTEKON_STUDIO_DIR="$PWD/studio/dist" \
  NOSTEKON_LAB_EXECUTABLE="$PWD/.venv/bin/nostekon" \
  NOSTEKON_LAB_DATA_DIR="$HOME/.local/share/nostekon/lab-jobs" \
  go run ./cmd/nostekon-api
```

Use an unused loopback port if `8181` is occupied. The executable path assumes
this guide's `.venv` installation; change it to the actual trusted CLI path.
The data directory is created with mode `0700`; an existing directory must be
private and must not be a symlink. Do not reuse another application's directory.
Open `http://127.0.0.1:8181/#/lab` and choose **Run suite**. Default settings are
10 writes and a 60-second tail-loss budget; input bounds remain 1..98 writes and
1..86400 seconds. This deletes source test namespaces after their backups, not
arbitrary workloads. Never point this feature at production clusters.

Only one job can run at a time. The session admits at most 50 jobs without
silently evicting prior results. Checkpoints expose case-level progress; process
output is the latest 64 KiB, marked when truncated. Output can be buffered until
the CLI finishes. A job's `completed` status means its process exited zero,
not that captures are authenticated. **Review suite** evaluates original case
JSON through the existing Go report API; saved cases remain Imported evidence
and signature-unverified.

**Cancel job**, the 15-minute timeout and graceful server shutdown send SIGINT
to the entire job process group. After 30 seconds, remaining processes are
forcibly stopped. SIGKILL cannot guarantee namespace cleanup or the final
checkpoint. Inspect retained files and namespace cleanup checks before starting
another run. Ordinary server restart clears session job discovery, but leaves
artifacts at `<data-directory>/<job-id>/suite/`; import those JSON files manually.
There is no automatic resume, cross-process job lock, history reindexing or
retention cleanup. Run only one enabled API instance for these shared lab clusters.

Lab API routes are disabled without both opt-in variables. Enabled requests
require a loopback listener, loopback Host and peer, same-origin browser context
and `X-Nostekon-Lab: true`; foreign origins/hosts and cross-site requests are
rejected. These checks prevent browser-driven cross-origin execution, not access
by other local programs or OS users. This is a trusted single-operator tool,
not authentication or multi-user authorization. Do not enable it behind a public
proxy or mount host credentials/Docker sockets into the default app container.

Verified on 2026-10-07 with real subprocess command, cancellation and timeout
tests; request-origin/input boundary tests; and desktop/mobile browser tests
using controlled job responses plus actual Go evidence evaluation. The default
Docker app's disabled mode was also tested. No live Kubernetes job was launched
as part of this verification.

### Review a suite in Studio

Open [Policy suite](/demo/index.html#/suite) to review the recorded three-case
PostgreSQL bundle. The original capture is stored unchanged under
`examples/suites/postgresql-policy/`.

To review your own completed run, choose **Import suite** and select these four
JSON files together from the suite output directory:

- `suite.json`
- `zero-loss.drillrun.json`
- `tail-loss.drillrun.json`
- `budget-loss.drillrun.json`

Failed or interrupted suites may have fewer cases and evidence files. Select
the summary and available JSON evidence; missing files remain visible as review
findings. SQLite ledgers are not imported. Imports are limited to four files,
a 64 KiB summary and 16 MiB per DrillRun.

Studio evaluates each DrillRun with the Go report engine and compares its exit
code and eight RPO fields with the summary. The view separates the runner's
suite result, each expected verdict and its independently evaluated verdict.
A strict tail-loss case can therefore show **Failed** while agreeing with the
suite's expected outcome. Each case opens in the existing report view for
inspection, original-evidence export and saving to the local run library. The
summary download preserves its original bytes.

Choose **Save cases** to save every captured case to **Runs** in one action.
Every case must have available evidence and an evaluated report; a **Failed**
verdict is still a valid report and can be saved. Summary mismatches remain
review findings and do not prevent saving otherwise valid case evidence.
Saving does not change the summary's runner result or agreement status.

The save is all-or-nothing. If the 50-run library has insufficient space or
browser storage fails, no cases are added and existing runs are unchanged.
Delete unneeded runs or resolve storage availability, then retry. Identical
original evidence is deduplicated, so repeated saves do not add extra copies.
Saved cases survive reload on the same browser origin. All cases saved this
way are labelled **Imported evidence**, including the built-in bundle; saving
does not add trusted provenance or verify signatures.

This saves individual DrillRuns, not the suite summary or case grouping.
Download the original summary and retain the input files to review the suite
again after reload. Clearing browser site data also removes the saved runs.

**Evidence matches the summary** means the submitted claims agree with the
evaluated JSON evidence. It does not authenticate the capture, inspect the
SQLite ledger, reproduce every runner acceptance check, verify signatures or
prove that a restore actually happened. Imported bundles remain labelled
**Imported evidence**; the built-in bundle is **Recorded local lab**. Both
remain signature-unverified. Follow the existing attestation workflow for
trusted-key verification outside the static demo.

Verified on 2026-10-05 in the browser demo at 390px and 1440px: all three
recorded outcomes, changed-summary detection, missing/invalid imports and
original-byte downloads. Suite-case saving, reload persistence, deduplication
and simulated storage-failure rollback/retry were checked in the browser.
Library-limit rollback and concurrent saves were checked with IndexedDB tests.

### Ledger And Measurement Limits

`--writes` defaults to one and must be positive; `--after-backup-writes`
defaults to zero and must be nonnegative. Together they are capped at 100.
`--rpo-seconds` defaults to `0` and accepts whole seconds from `0` to `86400`.
The declared budget is included in `spec.objectives.rpo`; it is not inferred
from the observed loss after the restore.
The runner records an acknowledgement only after `psql` returns success, then
commits it to the host-side SQLite ledger. The timestamp is when the host
observed success, not the database's internal commit time.

The `<output>.ledger.db` sidecar has private file permissions and must not
already exist, just like the evidence output. Keep it outside both clusters
and retain it with the evidence. If execution stops before restored IDs are
queried and validated, the sidecar retains completed acknowledgements, but the DrillRun
omits ledger measurements rather than claiming all writes were lost. A V3
row-count failure still emits V3-only evidence.

This measures loss for a bounded, sequential PostgreSQL test workload. It is
not application-level acknowledgement instrumentation, a business invariant,
PITR/WAL validation or authenticated provenance. The bundled historical V3
sample remains unchanged.

## 6. Teardown

To delete the test clusters when done:

```bash
make lab-down
```
