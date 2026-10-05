"""Repeatable policy regression suite for the disposable PostgreSQL lab."""

import json
import os
from datetime import datetime, timedelta
from pathlib import Path

from nostekon.lab import (
    DEFAULT_POSTGRES_IMAGE,
    DEFAULT_RESTORE_CONTEXT,
    DEFAULT_SOURCE_CONTEXT,
    execute_isolated_drill,
    utc_timestamp,
)
from nostekon.ledger import Ack, measure_rpo


def _evaluate_case(output: Path, writes: int, tail: int, budget: int, case: dict) -> None:
    evidence = json.loads(output.read_text())
    status = evidence["status"]
    ledger = status["ledger"]
    measurement = measure_rpo(
        [Ack(datetime.fromisoformat(ack["ackedAt"]), ack["writeId"]) for ack in ledger["acks"]],
        ledger["present"],
        datetime.fromisoformat(status["failureAt"]),
    )
    met = measurement.rpo <= timedelta(seconds=budget)
    case["rpo"] = {
        "acknowledged": measurement.acknowledged,
        "recovered": measurement.recovered,
        "lost": measurement.lost,
        "holes": measurement.holes,
        "unexpected": measurement.unexpected,
        "seconds": measurement.rpo.total_seconds(),
        "objectiveSeconds": budget,
        "met": met,
    }
    expected_error = (
        f"acknowledged-write ledger failed 0s RPO objective: {tail} lost, 0 holes, 0 unexpected"
    )
    negative = tail > 0 and budget == 0
    checks = status["checks"]
    allowed_failures = [
        check
        for check in checks
        if check.get("passed") is False
        and check.get("level") == "V4"
        and check.get("name") == "Lab execution"
        and check.get("detail") == expected_error
    ]
    case["passed"] = (
        case["observedExitCode"] == case["expectedExitCode"]
        and (case.get("error") == expected_error if negative else "error" not in case)
        and measurement.acknowledged == writes + tail
        and measurement.recovered == writes
        and measurement.lost == tail
        and measurement.holes == 0
        and measurement.unexpected == 0
        and len(ledger["present"]) == len(set(ledger["present"])) == writes
        and met is not negative
        and evidence["spec"].get("upTo") == "V4"
        and evidence["spec"].get("objectives", {}).get("rpo") == f"{budget}s"
        and {check.get("level") for check in checks if check.get("passed") is True}
        >= {"V0", "V1", "V2", "V3", "V4"}
        and any(
            check.get("level") == "V4"
            and check.get("name") == "probe-write-preserved"
            and check.get("passed") is True
            for check in checks
        )
        and all(
            check.get("passed") is True or (negative and check in allowed_failures)
            for check in checks
        )
        and (bool(allowed_failures) if negative else not allowed_failures)
    )
    if not case["passed"]:
        case["detail"] = "Observed evidence does not match this policy scenario"


def execute_lab_suite(
    output: Path,
    source_context: str = DEFAULT_SOURCE_CONTEXT,
    restore_context: str = DEFAULT_RESTORE_CONTEXT,
    image: str = DEFAULT_POSTGRES_IMAGE,
    *,
    write_count: int = 10,
    rpo_seconds: int = 60,
) -> dict:
    """Run zero loss, expected strict loss and explicitly budgeted tail loss."""
    if type(write_count) is not int or not 1 <= write_count <= 98:
        raise ValueError("suite writes must be a whole number between 1 and 98")
    if type(rpo_seconds) is not int or not 1 <= rpo_seconds <= 86400:
        raise ValueError("suite RPO budget must be whole seconds between 1 and 86400")
    output.mkdir(mode=0o700, parents=True)
    descriptor = os.open(output / "suite.json", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    summary = {
        "apiVersion": "nostekon/lab-suite/v1alpha1",
        "kind": "LabSuiteResult",
        "startedAt": utc_timestamp(),
        "completedAt": None,
        "status": "running",
        "passed": False,
        "cases": [],
    }
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:

        def persist() -> None:
            stream.seek(0)
            json.dump(summary, stream, indent=2)
            stream.write("\n")
            stream.truncate()
            stream.flush()
            os.fsync(stream.fileno())

        persist()
        try:
            for name, tail, budget in [
                ("zero-loss", 0, 0),
                ("tail-loss", 2, 0),
                ("budget-loss", 2, rpo_seconds),
            ]:
                case = {
                    "name": name,
                    "drillRun": f"{name}.drillrun.json",
                    "ledger": f"{name}.drillrun.json.ledger.db",
                    "expectedExitCode": 1 if name == "tail-loss" else 0,
                    "observedExitCode": None,
                    "passed": False,
                }
                summary["cases"].append(case)
                persist()
                evidence_path = output / case["drillRun"]
                try:
                    execute_isolated_drill(
                        evidence_path,
                        source_context,
                        restore_context,
                        image,
                        write_count=write_count,
                        after_backup_writes=tail,
                        rpo_seconds=budget,
                    )
                    case["observedExitCode"] = 0
                except KeyboardInterrupt:
                    case["observedExitCode"] = 130
                    case["error"] = "lab interrupted"
                    summary["status"] = "interrupted"
                    raise
                except Exception as error:
                    case["observedExitCode"] = 1
                    case["error"] = str(error)[:500]
                try:
                    _evaluate_case(evidence_path, write_count, tail, budget, case)
                except (OSError, ValueError, KeyError, TypeError) as error:
                    case["detail"] = f"No complete measured evidence: {error}"[:500]
                persist()
                if not case["passed"]:
                    break
            summary["passed"] = len(summary["cases"]) == 3 and all(
                case["passed"] for case in summary["cases"]
            )
            summary["status"] = "passed" if summary["passed"] else "failed"
        finally:
            summary["completedAt"] = utc_timestamp()
            persist()
    return summary
