---
title: Run locally
description: Validate a recovery drill with the local Go API and Studio.
---

The Studio is a local interactive application. Nothing in the validator creates, schedules, or runs a restore; it checks the JSON drill contract only. The Go API listens on port 8080 and the Studio development server proxies requests to it.

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

Open `http://127.0.0.1:5173`. Import a JSON drill, edit it, format it, validate it, and download the edited file. A green validation result means the document meets the current contract; it does **not** mean a recovery succeeded.

For the CLI, install the Python package in a virtual environment and validate the bundled YAML drills:

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -e ".[dev]"
checkride validate examples/drills/*.yaml
```

The API also accepts `POST /api/v1/drills/validate` with JSON. It returns `200` for a valid document, `422` for contract errors, `400` for malformed JSON, `413` for oversized bodies, and `415` for unsupported media types.