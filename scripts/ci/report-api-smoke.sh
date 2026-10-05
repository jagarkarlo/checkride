#!/usr/bin/env bash
set -euo pipefail

log_file=$(mktemp)
verified_report=$(mktemp)
failed_report=$(mktemp)
api_pid=""
cleanup() {
  if [[ -n "$api_pid" ]]; then
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
  fi
  rm -f "$log_file" "$verified_report" "$failed_report"
}
trap cleanup EXIT

go build -o /tmp/nostekon-api-smoke ./cmd/nostekon-api
/tmp/nostekon-api-smoke >"$log_file" 2>&1 &
api_pid=$!
for attempt in {1..30}; do
  if curl --fail --silent --show-error http://127.0.0.1:8080/healthz >/dev/null; then
    break
  fi
  if [[ "$attempt" == 30 ]]; then
    cat "$log_file" >&2
    exit 1
  fi
  sleep 1
done

curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  --data-binary @examples/runs/mlflow-namespace-loss.run.json \
  http://127.0.0.1:8080/api/v1/runs/report >"$verified_report"
jq -e '
  .verdict == "verified" and
  .requestedLevel == "V4" and
  .deepestPassed == "V4" and
  .rto.seconds == 703 and
  .rpo.seconds == 38 and
  .rpo.lost == 75
' "$verified_report" >/dev/null

curl --fail --silent --show-error \
  -H 'Content-Type: application/json' \
  --data-binary @examples/runs/crud-cluster-loss.run.json \
  http://127.0.0.1:8080/api/v1/runs/report >"$failed_report"
jq -e '
  .verdict == "failed" and
  .firstFailed == "V3" and
  .rto.met == false and
  (.findings | map(.message) | any(contains("table row counts")))
' "$failed_report" >/dev/null

schema_headers=$(mktemp)
curl --fail --silent --show-error -D "$schema_headers" \
  http://127.0.0.1:8080/api/v1/schemas/drillrun >/tmp/nostekon-drillrun-schema.json
trap 'rm -f "$schema_headers" /tmp/nostekon-drillrun-schema.json; cleanup' EXIT
grep -qi '^Content-Type: application/schema+json' "$schema_headers"
jq -e '(.title == "Nostekon DrillRun") and ((.properties.status.required // []) | index("completedAt") != null)' \
  /tmp/nostekon-drillrun-schema.json >/dev/null

set +e
go run ./cmd/nostekon-report examples/runs/crud-cluster-loss.run.json >/dev/null
failed_exit=$?
go run ./cmd/nostekon-report examples/runs/mlflow-namespace-loss.run.json >/dev/null
verified_exit=$?
set -e
test "$failed_exit" -eq 1
test "$verified_exit" -eq 0

printf '%s\n' 'Nostekon report API smoke checks passed.'
