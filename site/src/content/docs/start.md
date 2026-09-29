---
title: Run locally
description: Validate a recovery drill with the local Go API and Studio.
---

The Studio is a local interactive application. Drill-spec validation checks the contract only. The separate DrillRun report evaluator calculates a verdict from caller-supplied evidence; it does not authenticate the evidence. The Go API listens on port 8080 and the Studio development server proxies requests to it. A separate disposable k3d script runs one isolated PostgreSQL restore; Studio does not trigger it.

From the repository root, start the API:

```bash
go run ./cmd/checkride-api
```

In another terminal, start the Studio:

```bash
cd studio
npm ci
npm run dev
```

Open `http://127.0.0.1:5173`. The **Design drill** view lets you import a JSON drill, edit it, format it, validate it, and download the edited file. A green result means the specification meets the current contract; it does **not** mean a recovery succeeded.

The **Evidence report** view includes two synthetic runs and one locally recorded k3d lab run. Import a recorded DrillRun JSON file or build a report from a sample. Select a V0–V4 level to inspect its checks, review the phase timeline and write-loss window, and export the report as JSON or Markdown. The report evaluates caller-supplied evidence and cannot establish who captured those observations.

## Run the isolated PostgreSQL lab drill

Verified on 2026-09-29 with k3d v5.8.3, k3s v1.35.8-k3s1, PostgreSQL 16.8 and Docker 28.3.2. Requires Docker, k3d, kubectl, Python 3.12+ and Go. Use only the two disposable Checkride k3d clusters: the script creates and deletes uniquely named namespaces and **deletes the source namespace after taking the dump**. Never point these kube context names at shared clusters.

From the repository root:

```bash
make lab-up
python3 scripts/lab/restore.py --output /tmp/checkride-drill.json
go run ./cmd/checkride-report /tmp/checkride-drill.json
make lab-down
```

The runner checks that both contexts resolve to different clusters before creating anything. It starts a PostgreSQL pod in each, inserts a random write in the source, captures `pg_dump` locally, deletes the source namespace, replays the dump into the restore cluster, checks connectivity and compares the recovered write. The output contains runner-reported V0–V3 checks and recovery phases. A successful report says `"verdict": "verified"` and `"deepestPassed": "V3"`; V4, RPO and provenance are **not** established. The [browser demo](/demo/index.html#/report) runs the full Studio and Go evaluator in WebAssembly without accessing Kubernetes; it includes the recorded lab evidence alongside synthetic examples.

The runner attempts namespace cleanup on failure and raises a nonzero exit status. If it reports a cleanup error, inspect the named `checkride-*` namespace in the named k3d context; do not assume it was deleted. `make lab-down` removes only the two named disposable clusters. On proxy-restricted hosts, Kubernetes nodes may not reach Docker Hub; pull the required images through the host Docker daemon and use `k3d image import -c checkride-source -c checkride-restore IMAGE...` before retrying. At minimum PostgreSQL 16.8 and `rancher/mirrored-pause:3.10.2` must be available; missing CoreDNS or metrics-server images can also block cluster health and namespace finalization.

For the CLI, install the Python package in a virtual environment and validate the bundled YAML drills:

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
checkride validate examples/drills/*.yaml
```

To evaluate a recorded DrillRun from the CLI:

```bash
go run ./cmd/checkride-report examples/runs/mlflow-namespace-loss.run.json
```

The command prints a JSON report. Exit status is `0` for verified, `1` for failed and `2` for incomplete or invalid evidence, so it can later be used as a CI gate. The current CLI report command is a separate Go binary and requires Go; the Python CLI still handles drill specs and acknowledged-write RPO calculations.

The API also accepts `POST /api/v1/drills/validate` with JSON. It returns `200` for a valid document, `422` for contract errors, `400` for malformed JSON, `413` for oversized bodies, and `415` for unsupported media types. `GET /api/v1/schemas/drillrun` returns the versioned DrillRun JSON Schema. `POST /api/v1/runs/report` accepts a DrillRun JSON document of up to 16 MiB and returns the report. Malformed or unknown JSON returns `400`, invalid evidence `422`, oversized bodies `413`, and other media types `415`. Four report computations may run concurrently; additional requests return `503` with `Retry-After`.