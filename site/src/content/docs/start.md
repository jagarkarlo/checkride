---
title: Run locally
description: Validate a recovery drill with the local Go API and Studio.
---

The Studio is a local interactive application. Drill-spec validation checks the contract only. The separate DrillRun report evaluator calculates a verdict from caller-supplied evidence; it does not authenticate the evidence or execute a restore. The Go API listens on port 8080 and the Studio development server proxies requests to it.

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

The **Evidence report** view starts with two illustrative runs. The examples are synthetic, not records from a real cluster. Import a recorded DrillRun JSON file or build a report from a sample. Select a V0–V4 level to inspect its checks, review the phase timeline and write-loss window, and export the report as JSON or Markdown. The report evaluates caller-supplied evidence; until the orchestrator is implemented, it cannot establish who captured those observations.

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