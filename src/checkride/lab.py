"""Isolated k3d cluster recovery drill runner."""

import json
import os
import sqlite3
import subprocess
import sys
from datetime import UTC, datetime
from pathlib import Path
from urllib.parse import urlsplit
from uuid import uuid4

from checkride.ledger import Ledger

DEFAULT_SOURCE_CONTEXT = "k3d-checkride-source"
DEFAULT_RESTORE_CONTEXT = "k3d-checkride-restore"
DEFAULT_POSTGRES_IMAGE = "postgres:16.8"


def utc_timestamp() -> str:
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def run_kubectl(
    context: str,
    namespace: str | None,
    *args: str,
    input_data: bytes | None = None,
) -> bytes:
    command = ["kubectl", "--context", context, "--request-timeout=200s"]
    if namespace:
        command.extend(["-n", namespace])
    command.extend(args)
    env = os.environ.copy()
    for key in ("NO_PROXY", "no_proxy"):
        env[key] = ",".join(filter(None, (env.get(key), "0.0.0.0,127.0.0.1,localhost")))
    return subprocess.run(
        command, input=input_data, capture_output=True, check=True, env=env, timeout=210
    ).stdout


def check_cluster_health(context: str) -> dict:
    """Check if a k3d or Kubernetes cluster is reachable and healthy."""
    try:
        uid = (
            run_kubectl(
                context, None, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}"
            )
            .decode()
            .strip()
        )
        nodes_raw = (
            run_kubectl(
                context,
                None,
                "get",
                "nodes",
                "-o",
                "jsonpath={.items[*].status.conditions[?(@.type=='Ready')].status}",
            )
            .decode()
            .strip()
        )
        ready_nodes = nodes_raw.split().count("True")
        return {
            "context": context,
            "reachable": True,
            "uid": uid,
            "ready_nodes": ready_nodes,
            "total_nodes": len(nodes_raw.split()),
        }
    except Exception as exc:
        return {
            "context": context,
            "reachable": False,
            "error": str(exc)[:200],
            "ready_nodes": 0,
            "total_nodes": 0,
        }


def execute_isolated_drill(
    output: Path,
    source_context: str = DEFAULT_SOURCE_CONTEXT,
    restore_context: str = DEFAULT_RESTORE_CONTEXT,
    image: str = DEFAULT_POSTGRES_IMAGE,
) -> dict:
    """Execute a PostgreSQL logical backup and restore across two distinct k3d clusters."""
    for context in (source_context, restore_context):
        if not context.startswith("k3d-checkride-"):
            raise RuntimeError("only local k3d-checkride-* lab contexts are allowed")
        server = (
            run_kubectl(
                context,
                None,
                "config",
                "view",
                "--minify",
                "-o",
                "jsonpath={.clusters[0].cluster.server}",
            )
            .decode()
            .strip()
        )
        endpoint = urlsplit(server)
        if endpoint.scheme != "https" or endpoint.hostname not in (
            "localhost",
            "127.0.0.1",
            "0.0.0.0",
            "::1",
        ):
            raise RuntimeError("lab contexts must address a local Kubernetes API endpoint")
    identities = [
        run_kubectl(
            context, None, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}"
        ).strip()
        for context in (source_context, restore_context)
    ]
    if not all(identities) or identities[0] == identities[1]:
        raise RuntimeError("source and restore contexts must address distinct clusters")

    output.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(descriptor)
    ledger_path = output.with_name(output.name + ".ledger.db")
    try:
        descriptor = os.open(ledger_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(descriptor)
    except OSError:
        output.unlink()
        raise
    namespace = f"checkride-{uuid4().hex[:12]}"
    checks: list[dict] = []
    phases: list[dict] = []
    failure_at: str | None = None
    active_level = "V0"
    requested_level = "V3"
    has_v4_evidence = False
    ledger: Ledger | None = None
    acknowledged: list[dict] = []
    present: list[str] | None = None
    created: list[str] = []
    error: BaseException | None = None

    def record_phase(name: str, action):
        started_at = utc_timestamp()
        try:
            return action()
        finally:
            phases.append({"name": name, "startedAt": started_at, "endedAt": utc_timestamp()})

    try:
        ledger = Ledger(ledger_path)
        for context in (source_context, restore_context):
            run_kubectl(context, None, "create", "namespace", namespace)
            created.append(context)
            run_kubectl(
                context,
                namespace,
                "run",
                "postgres",
                f"--image={image}",
                "--restart=Never",
                "--overrides="
                + json.dumps(
                    {
                        "spec": {
                            "automountServiceAccountToken": False,
                            "terminationGracePeriodSeconds": 5,
                            "containers": [
                                {
                                    "name": "postgres",
                                    "image": image,
                                    "env": [
                                        {"name": "POSTGRES_HOST_AUTH_METHOD", "value": "trust"}
                                    ],
                                    "args": ["postgres", "-c", "listen_addresses=127.0.0.1"],
                                    "readinessProbe": {
                                        "exec": {
                                            "command": [
                                                "pg_isready",
                                                "-h",
                                                "127.0.0.1",
                                                "-U",
                                                "postgres",
                                            ]
                                        },
                                        "periodSeconds": 2,
                                    },
                                    "resources": {
                                        "requests": {"cpu": "100m", "memory": "128Mi"},
                                        "limits": {"cpu": "1", "memory": "512Mi"},
                                    },
                                }
                            ],
                        }
                    }
                ),
            )
            run_kubectl(
                context,
                namespace,
                "wait",
                "--for=condition=Ready",
                "pod/postgres",
                "--timeout=180s",
            )

        write_id = uuid4().hex

        def backup():
            run_kubectl(
                source_context,
                namespace,
                "exec",
                "postgres",
                "--",
                "psql",
                "-U",
                "postgres",
                "-v",
                "ON_ERROR_STOP=1",
                "-c",
                "CREATE TABLE recovery_probe (write_id text PRIMARY KEY)",
            )
            run_kubectl(
                source_context,
                namespace,
                "exec",
                "postgres",
                "--",
                "psql",
                "-U",
                "postgres",
                "-v",
                "ON_ERROR_STOP=1",
                "-c",
                f"INSERT INTO recovery_probe VALUES ('{write_id}')",
            )
            acked_at = datetime.now(UTC)
            ledger.record(write_id, acked_at)
            acknowledged.append({"writeId": write_id, "ackedAt": acked_at.isoformat()})
            dump = run_kubectl(
                source_context,
                namespace,
                "exec",
                "postgres",
                "--",
                "pg_dump",
                "-U",
                "postgres",
                "--no-owner",
                "--no-privileges",
                "postgres",
            )
            if not dump:
                raise RuntimeError("pg_dump produced no data")
            checks.append({"level": "V0", "name": "PostgreSQL logical backup", "passed": True})
            return dump

        dump = backup()
        failure_at = utc_timestamp()
        active_level = "V1"
        record_phase(
            "source loss",
            lambda: run_kubectl(
                source_context,
                None,
                "delete",
                "namespace",
                namespace,
                "--wait=true",
                "--timeout=120s",
            ),
        )
        created.remove(source_context)

        def restore():
            run_kubectl(
                restore_context,
                namespace,
                "exec",
                "-i",
                "postgres",
                "--",
                "psql",
                "-U",
                "postgres",
                "-v",
                "ON_ERROR_STOP=1",
                input_data=dump,
            )
            checks.append({"level": "V1", "name": "PostgreSQL logical restore", "passed": True})

        record_phase("restore", restore)
        active_level = "V2"

        def verify():
            nonlocal active_level, requested_level, has_v4_evidence, present
            run_kubectl(
                restore_context, namespace, "exec", "postgres", "--", "pg_isready", "-U", "postgres"
            )
            checks.append(
                {"level": "V2", "name": "Restored PostgreSQL accepts connections", "passed": True}
            )
            active_level = "V3"
            row_count = (
                run_kubectl(
                    restore_context,
                    namespace,
                    "exec",
                    "postgres",
                    "--",
                    "psql",
                    "-U",
                    "postgres",
                    "-At",
                    "-v",
                    "ON_ERROR_STOP=1",
                    "-c",
                    "SELECT count(*) FROM recovery_probe",
                )
                .decode()
                .strip()
            )
            if row_count != "1":
                checks.append(
                    {
                        "level": "V3",
                        "name": "recovery_probe contains exactly one row",
                        "passed": False,
                        "detail": f"expected 1 row, got {row_count or 'no result'}",
                    }
                )
                raise RuntimeError("restored recovery_probe row count does not match")
            checks.append(
                {
                    "level": "V3",
                    "name": "recovery_probe contains exactly one row",
                    "passed": True,
                    "detail": "recovered row count is 1",
                }
            )
            requested_level = "V4"
            has_v4_evidence = True
            active_level = "V4"
            found = (
                run_kubectl(
                    restore_context,
                    namespace,
                    "exec",
                    "postgres",
                    "--",
                    "psql",
                    "-U",
                    "postgres",
                    "-At",
                    "-v",
                    "ON_ERROR_STOP=1",
                    "-c",
                    "SELECT write_id FROM recovery_probe",
                )
                .decode()
                .strip()
            )
            present = found.splitlines() if found else []
            checks.append(
                {
                    "level": "V4",
                    "name": "probe-write-preserved",
                    "passed": found == write_id,
                    "detail": (
                        "restored write ID matches"
                        if found == write_id
                        else "restored write ID does not match the source write"
                    ),
                }
            )
            if found != write_id:
                raise RuntimeError("restored write does not match source write")
            return found

        record_phase("verify", verify)
    except (
        RuntimeError,
        subprocess.SubprocessError,
        OSError,
        sqlite3.Error,
        KeyboardInterrupt,
    ) as exc:
        error = exc
        if not any(check["level"] == active_level and not check["passed"] for check in checks):
            checks.append(
                {
                    "level": active_level,
                    "name": "Lab execution",
                    "passed": False,
                    "detail": str(exc)[:500],
                }
            )
    finally:
        completed_at = utc_timestamp()
        failure_at = failure_at or completed_at
        if has_v4_evidence and not any(
            check["level"] == "V4" and check["name"] == "probe-write-preserved" for check in checks
        ):
            checks.append(
                {
                    "level": "V4",
                    "name": "probe-write-preserved",
                    "passed": False,
                    "detail": (
                        "not evaluated because the lab run stopped before the application "
                        "invariant check"
                    ),
                }
            )
        for context in reversed(created):
            try:
                run_kubectl(
                    context,
                    None,
                    "delete",
                    "namespace",
                    namespace,
                    "--wait=true",
                    "--timeout=120s",
                )
            except (RuntimeError, subprocess.SubprocessError, OSError, KeyboardInterrupt) as exc:
                print(f"Cleanup failed for {context}/{namespace}: {exc}", file=sys.stderr)
                checks.append(
                    {
                        "level": "V3",
                        "name": "Lab namespace cleanup",
                        "passed": False,
                        "detail": str(exc)[:500],
                    }
                )
                error = error or exc
        result = {
            "apiVersion": "checkride/v1alpha1",
            "kind": "DrillRun",
            "metadata": {"name": namespace},
            "spec": {"scenario": "isolated-postgresql-logical-restore", "upTo": requested_level},
            "status": {
                "failureAt": failure_at,
                "completedAt": completed_at,
                "phases": phases,
                "checks": checks,
            },
        }
        if has_v4_evidence:
            result["spec"]["v4Evidence"] = {"invariants": ["probe-write-preserved"]}
        if present is not None:
            result["status"]["ledger"] = {"acks": acknowledged, "present": present}
        if ledger is not None:
            ledger.close()
        output.write_text(json.dumps(result, indent=2) + "\n")
        print(f"Recorded lab evidence in {output}")
    if error:
        raise error
    return result
