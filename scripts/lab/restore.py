#!/usr/bin/env python3
"""Run a disposable PostgreSQL dump/restore across the two Checkride k3d clusters."""

import argparse
import json
import os
import subprocess
import sys
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

SOURCE = "k3d-checkride-source"
RESTORE = "k3d-checkride-restore"
IMAGE = "postgres:16.8"


def timestamp():
    return datetime.now(UTC).isoformat().replace("+00:00", "Z")


def kubectl(context, namespace, *args, input_data=None):
    command = ["kubectl", "--context", context]
    if namespace:
        command.extend(["-n", namespace])
    command.extend(args)
    environment = os.environ.copy()
    for key in ("NO_PROXY", "no_proxy"):
        environment[key] = ",".join(filter(None, (environment.get(key), "0.0.0.0")))
    return subprocess.run(
        command, input=input_data, capture_output=True, check=True, env=environment
    ).stdout


def run(output):
    identities = [
        kubectl(context, None, "get", "namespace", "kube-system", "-o", "jsonpath={.metadata.uid}")
        for context in (SOURCE, RESTORE)
    ]
    if not all(identities) or identities[0] == identities[1]:
        raise RuntimeError("source and restore contexts must address distinct clusters")

    namespace = "checkride-" + uuid4().hex[:12]
    checks = []
    phases = []
    failure_at = None
    active_level = "V0"
    created = []
    error = None

    def phase(name, action):
        started_at = timestamp()
        try:
            return action()
        finally:
            phases.append({"name": name, "startedAt": started_at, "endedAt": timestamp()})

    try:
        for context in (SOURCE, RESTORE):
            kubectl(context, None, "create", "namespace", namespace)
            created.append(context)
            kubectl(
                context,
                namespace,
                "run",
                "postgres",
                "--image=" + IMAGE,
                "--env=POSTGRES_HOST_AUTH_METHOD=trust",
                "--port=5432",
            )
            kubectl(
                context,
                namespace,
                "wait",
                "--for=condition=Ready",
                "pod/postgres",
                "--timeout=180s",
            )

        write_id = uuid4().hex

        def backup():
            kubectl(
                SOURCE,
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
            kubectl(
                SOURCE,
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
            dump = kubectl(
                SOURCE,
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
        failure_at = timestamp()
        active_level = "V1"
        phase(
            "source loss",
            lambda: kubectl(
                SOURCE, None, "delete", "namespace", namespace, "--wait=true", "--timeout=120s"
            ),
        )
        created.remove(SOURCE)

        def restore():
            kubectl(
                RESTORE,
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

        phase("restore", restore)
        active_level = "V2"

        def verify():
            nonlocal active_level
            kubectl(RESTORE, namespace, "exec", "postgres", "--", "pg_isready", "-U", "postgres")
            checks.append(
                {"level": "V2", "name": "Restored PostgreSQL accepts connections", "passed": True}
            )
            active_level = "V3"
            found = (
                kubectl(
                    RESTORE,
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
            if found != write_id:
                raise RuntimeError("restored write does not match source write")
            checks.append(
                {
                    "level": "V3",
                    "name": "Restored table contains exactly the source write",
                    "passed": True,
                }
            )
            return found

        phase("verify", verify)
    except (RuntimeError, subprocess.CalledProcessError, KeyboardInterrupt) as exc:
        error = exc
        checks.append(
            {
                "level": active_level,
                "name": "Lab execution",
                "passed": False,
                "detail": str(exc)[:500],
            }
        )
    finally:
        completed_at = timestamp()
        for context in reversed(created):
            try:
                kubectl(
                    context, None, "delete", "namespace", namespace, "--wait=true", "--timeout=120s"
                )
            except subprocess.CalledProcessError as exc:
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
            "spec": {"scenario": "isolated-postgresql-logical-restore", "upTo": "V3"},
            "status": {
                "failureAt": failure_at or timestamp(),
                "completedAt": completed_at,
                "phases": phases,
                "checks": checks,
            },
        }
        output.write_text(json.dumps(result, indent=2) + "\n")
        print(f"Recorded lab evidence in {output}")
    if error:
        raise error


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="local DrillRun JSON path")
    arguments = parser.parse_args()
    run(arguments.output)
