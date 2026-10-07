"""Command-boundary tests for the disposable k3d restore runner."""

import json
import os
import select
import shutil
import signal
import stat
import subprocess
import sys
from datetime import datetime
from pathlib import Path
from unittest.mock import patch

import pytest
import yaml

from nostekon import lab as restore
from nostekon.ledger import Ledger

SOURCE = restore.DEFAULT_SOURCE_CONTEXT
RESTORE = restore.DEFAULT_RESTORE_CONTEXT


def fake_kubectl(commands, mismatch=False, row_count=None):
    backup_ids = []

    def execute(context, namespace, *args, input_data=None):
        commands.append((context, namespace, args, input_data))
        if args[:2] == ("config", "view"):
            return b"https://127.0.0.1:6443"
        if args[:2] == ("get", "namespace"):
            return context.encode()
        if "pg_dump" in args:
            backup_ids.clear()
            backup_ids.extend(
                arg.split("'")[1]
                for _, recorded_namespace, recorded_args, _ in commands
                if recorded_namespace == namespace
                for arg in recorded_args
                if arg.startswith("INSERT INTO")
            )
            return b"CREATE TABLE recovery_probe ...;"
        if "SELECT count(*) FROM recovery_probe" in args:
            return row_count if row_count is not None else str(len(backup_ids)).encode()
        if "SELECT write_id FROM recovery_probe" in args:
            return b"wrong" if mismatch else "\n".join(backup_ids).encode()
        return b""

    return execute


def commands_write_id(commands):
    insert = next(
        arg for _, _, args, _ in commands for arg in args if arg.startswith("INSERT INTO")
    )
    return insert.split("'")[1]


def test_lab_workflow_exercises_the_suite_runner():
    workflow = yaml.safe_load(
        (Path(__file__).parent.parent / ".github/workflows/lab.yml").read_text()
    )
    commands = "\n".join(step.get("run", "") for step in workflow["jobs"]["restore"]["steps"])
    assert "nostekon lab suite --writes 10 --rpo-seconds 60" in commands
    assert "nostekon lab run" not in commands


@pytest.fixture(scope="module")
def lab_gate_workspace(tmp_path_factory):
    if any(shutil.which(tool) is None for tool in ("go", "jq", "bash")):
        pytest.skip("workflow contract checks need Go, jq and bash")
    repository = Path(__file__).parent.parent
    workspace = tmp_path_factory.mktemp("lab-gate")
    for name in ("cmd", "internal"):
        shutil.copytree(repository / name, workspace / name)
    shutil.copy2(repository / "go.mod", workspace / "go.mod")
    shutil.copytree(
        repository / "examples/suites/postgresql-policy", workspace / "lab-results/suite"
    )
    runner = workspace / "runner"
    runner.mkdir()
    workflow = yaml.safe_load((repository / ".github/workflows/lab.yml").read_text())
    steps = {
        step["name"]: step["run"] for step in workflow["jobs"]["restore"]["steps"] if "run" in step
    }
    environment = {**os.environ, "RUNNER_TEMP": str(runner)}
    for name in (
        "Evaluate V4 evidence",
        "Verify deliberate acknowledged-write loss",
        "Verify acknowledged-write loss within an explicit RPO budget",
    ):
        assert "nostekon lab" not in steps[name]
        result = subprocess.run(
            ["bash", "-e", "-o", "pipefail"],
            input=steps[name],
            cwd=workspace,
            env=environment,
            capture_output=True,
            text=True,
            timeout=120,
        )
        assert result.returncode == 0, result.stderr
    return workspace, steps


@pytest.mark.parametrize(
    "change",
    [
        "unchanged",
        "acknowledged",
        "recovered",
        "lost",
        "holes",
        "unexpected",
        "seconds",
        "objectiveSeconds",
        "met",
        "within-tolerance",
        "exit",
        "missing-case",
        "status",
        "case-passed",
        "order",
    ],
)
def test_lab_workflow_compares_suite_summary_to_go_reports(lab_gate_workspace, change):
    workspace, steps = lab_gate_workspace
    source = workspace / "lab-results/suite/suite.json"
    original = source.read_text()
    summary = json.loads(original)
    if change in ("acknowledged", "recovered", "lost", "holes", "unexpected", "objectiveSeconds"):
        summary["cases"][2]["rpo"][change] += 1
    elif change == "seconds":
        summary["cases"][2]["rpo"]["seconds"] += 0.01
    elif change == "within-tolerance":
        summary["cases"][2]["rpo"]["seconds"] += 0.0000005
    elif change == "met":
        summary["cases"][2]["rpo"]["met"] = False
    elif change == "exit":
        summary["cases"][1]["observedExitCode"] = 0
    elif change == "missing-case":
        summary["cases"].pop()
    elif change == "status":
        summary["status"] = "running"
    elif change == "case-passed":
        summary["cases"][2]["passed"] = False
    elif change == "order":
        summary["cases"].reverse()
    try:
        source.write_text(json.dumps(summary))
        result = subprocess.run(
            ["bash", "-e", "-o", "pipefail"],
            input=steps["Verify suite summary against Go reports"],
            cwd=workspace,
            capture_output=True,
            text=True,
            timeout=20,
        )
    finally:
        source.write_text(original)
    assert (result.returncode == 0) is (change in ("unchanged", "within-tolerance")), result.stderr


def test_lab_suite_measures_all_three_policy_outcomes(tmp_path):
    from nostekon.lab_suite import execute_lab_suite

    commands = []
    output = tmp_path / "suite"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        result = execute_lab_suite(output, write_count=3)
    assert result["passed"] is True
    assert result["apiVersion"] == "nostekon/lab-suite/v1alpha1"
    assert json.loads((output / "suite.json").read_text()) == result
    assert output.stat().st_mode & 0o777 == 0o700
    assert (output / "suite.json").stat().st_mode & 0o777 == 0o600
    cases = result["cases"]
    assert [case["name"] for case in cases] == ["zero-loss", "tail-loss", "budget-loss"]
    assert [case["observedExitCode"] for case in cases] == [0, 1, 0]
    assert [case["rpo"]["lost"] for case in cases] == [0, 2, 2]
    assert [case["rpo"]["objectiveSeconds"] for case in cases] == [0, 0, 60]
    assert [case["rpo"]["met"] for case in cases] == [True, False, True]
    for case in cases:
        assert case["passed"] is True
        assert case["rpo"]["recovered"] == 3
        assert (output / case["drillRun"]).is_file()
        assert json.loads((output / case["drillRun"]).read_text())["apiVersion"] == (
            "nostekon/v1alpha1"
        )
        assert (output / case["ledger"]).is_file()


@pytest.mark.parametrize("fault", ["restore-error", "cleanup-error", "unexpected-pass"])
def test_lab_suite_never_accepts_unrelated_failure_as_expected_tail_loss(tmp_path, fault):
    from nostekon import lab_suite

    original = restore.execute_isolated_drill
    calls = []

    def execute(output, *args, **kwargs):
        calls.append(output.name)
        commands = []
        kubectl = fake_kubectl(commands)

        def faulty(context, namespace, *command, input_data=None):
            if output.name.startswith("tail-loss"):
                if fault == "restore-error" and input_data is not None:
                    raise RuntimeError("restore transport broke")
                if (
                    fault == "cleanup-error"
                    and context == RESTORE
                    and command[:2] == ("delete", "namespace")
                ):
                    raise RuntimeError("cleanup transport broke")
            return kubectl(context, namespace, *command, input_data=input_data)

        if output.name.startswith("tail-loss") and fault == "unexpected-pass":
            kwargs["after_backup_writes"] = 0
        with patch.object(restore, "run_kubectl", side_effect=faulty):
            return original(output, *args, **kwargs)

    output = tmp_path / "suite"
    with patch.object(lab_suite, "execute_isolated_drill", side_effect=execute):
        result = lab_suite.execute_lab_suite(output, write_count=3)
    assert result["passed"] is False
    assert result["status"] == "failed"
    assert [case["passed"] for case in result["cases"]] == [True, False]
    assert calls == ["zero-loss.drillrun.json", "tail-loss.drillrun.json"]
    assert json.loads((output / "suite.json").read_text()) == result


def test_lab_suite_records_interruption_and_stops(tmp_path):
    from nostekon import lab_suite

    output = tmp_path / "suite"
    with (
        patch.object(lab_suite, "execute_isolated_drill", side_effect=KeyboardInterrupt),
        pytest.raises(KeyboardInterrupt),
    ):
        lab_suite.execute_lab_suite(output)
    result = json.loads((output / "suite.json").read_text())
    assert result["status"] == "interrupted"
    assert result["passed"] is False
    assert result["completedAt"] is not None
    assert len(result["cases"]) == 1
    assert result["cases"][0]["observedExitCode"] == 130


def test_lab_suite_failed_checkpoint_preserves_last_complete_summary(tmp_path):
    from nostekon import lab_suite

    output = tmp_path / "suite"
    previous = []
    original_dump = json.dump

    def failing_dump(value, stream, **kwargs):
        if value["cases"]:
            if not previous:
                previous.append((output / "suite.json").read_bytes())
            stream.write('{"kind":')
            raise OSError("checkpoint storage failed")
        return original_dump(value, stream, **kwargs)

    with (
        patch.object(lab_suite.json, "dump", side_effect=failing_dump),
        patch.object(lab_suite, "execute_isolated_drill") as execute,
        pytest.raises(OSError, match="checkpoint storage failed"),
    ):
        lab_suite.execute_lab_suite(output)
    execute.assert_not_called()
    assert (output / "suite.json").read_bytes() == previous[0]
    assert json.loads(previous[0])["cases"] == []
    assert set(output.iterdir()) == {output / "suite.json"}


@pytest.mark.parametrize("stage", ["file-sync", "replace"])
def test_lab_suite_checkpoint_publication_failure_preserves_snapshot(tmp_path, stage):
    from nostekon import lab_suite

    output = tmp_path / "suite"
    previous = []
    original = lab_suite.os.fsync if stage == "file-sync" else lab_suite.os.replace

    def failed_publication(*args):
        if stage == "file-sync" and not stat.S_ISREG(os.fstat(args[0]).st_mode):
            return original(*args)
        checkpoint = output / "suite.json"
        if previous or (checkpoint.is_file() and checkpoint.stat().st_size):
            if not previous:
                previous.append(checkpoint.read_bytes())
            raise OSError("checkpoint publication failed")
        return original(*args)

    target = "fsync" if stage == "file-sync" else "replace"
    with (
        patch.object(lab_suite.os, target, side_effect=failed_publication),
        patch.object(lab_suite, "execute_isolated_drill") as execute,
        pytest.raises(OSError, match="checkpoint publication failed"),
    ):
        lab_suite.execute_lab_suite(output)
    execute.assert_not_called()
    assert (output / "suite.json").read_bytes() == previous[0]
    assert json.loads(previous[0])["status"] == "running"
    assert set(output.iterdir()) == {output / "suite.json"}
    assert (output / "suite.json").stat().st_mode & 0o777 == 0o600


def test_lab_suite_directory_sync_failure_stops_before_execution(tmp_path):
    from nostekon import lab_suite

    original = lab_suite.os.fsync

    def failed_directory_sync(descriptor):
        if stat.S_ISDIR(os.fstat(descriptor).st_mode):
            raise OSError("checkpoint directory sync failed")
        return original(descriptor)

    output = tmp_path / "suite"
    with (
        patch.object(lab_suite.os, "fsync", side_effect=failed_directory_sync),
        patch.object(lab_suite, "execute_isolated_drill") as execute,
        pytest.raises(OSError, match="checkpoint directory sync failed"),
    ):
        lab_suite.execute_lab_suite(output)
    execute.assert_not_called()
    assert json.loads((output / "suite.json").read_text())["cases"] == []
    assert set(output.iterdir()) == {output / "suite.json"}


@pytest.mark.skipif(os.name != "posix", reason="POSIX process and directory-sync contract")
def test_lab_suite_hard_stop_during_checkpoint_keeps_complete_snapshot(tmp_path):
    output = tmp_path / "suite"
    code = """
import json
import signal
import sys
from pathlib import Path
from nostekon import lab_suite

original = json.dump
def paused_dump(value, stream, **kwargs):
    if value['cases']:
        stream.write('{"kind":')
        stream.flush()
        print('checkpoint-ready', flush=True)
        signal.pause()
    return original(value, stream, **kwargs)
lab_suite.json.dump = paused_dump
lab_suite.execute_lab_suite(Path(sys.argv[1]))
"""
    with subprocess.Popen(
        [sys.executable, "-c", code, str(output)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    ) as process:
        try:
            assert select.select([process.stdout], [], [], 10)[0]
            assert process.stdout.readline() == "checkpoint-ready\n"
            previous = (output / "suite.json").read_bytes()
            assert json.loads(previous)["cases"] == []
            process.kill()
            assert process.wait(timeout=10) == -signal.SIGKILL
        finally:
            if process.poll() is None:
                process.kill()
            process.communicate(timeout=10)
    assert (output / "suite.json").read_bytes() == previous
    temporary = list(output.glob(".suite-*.tmp"))
    assert len(temporary) == 1
    assert temporary[0].stat().st_mode & 0o777 == 0o600


@pytest.mark.parametrize("interrupted_case", [0, 1, 2])
def test_lab_suite_records_interruption_during_evidence_evaluation(tmp_path, interrupted_case):
    from nostekon import lab_suite

    commands = []
    output = tmp_path / "suite"
    evaluate = lab_suite._evaluate_case
    case_names = ["zero-loss", "tail-loss", "budget-loss"]

    def cancelled_evaluation(evidence_path, writes, tail, budget, case):
        if case["name"] == case_names[interrupted_case]:
            raise KeyboardInterrupt
        return evaluate(evidence_path, writes, tail, budget, case)

    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)),
        patch.object(lab_suite, "_evaluate_case", side_effect=cancelled_evaluation),
        pytest.raises(KeyboardInterrupt),
    ):
        lab_suite.execute_lab_suite(output)
    result = json.loads((output / "suite.json").read_text())
    assert result["status"] == "interrupted"
    assert result["passed"] is False
    assert result["completedAt"] is not None
    assert len(result["cases"]) == interrupted_case + 1
    assert all(case["passed"] for case in result["cases"][:-1])
    case = result["cases"][-1]
    assert case["observedExitCode"] == [0, 1, 0][interrupted_case]
    assert case["passed"] is False
    assert case["error"] == "lab interrupted"
    assert (output / case["drillRun"]).is_file()
    assert (output / case["ledger"]).is_file()
    assert len([item for item in commands if item[2][:2] == ("create", "namespace")]) == (
        2 * (interrupted_case + 1)
    )


def test_lab_suite_cleanup_interrupt_preserves_completed_cases(tmp_path):
    from nostekon import lab_suite

    commands = []
    execute = fake_kubectl(commands)
    backups = 0

    def cancelled_cleanup(context, namespace, *args, input_data=None):
        nonlocal backups
        result = execute(context, namespace, *args, input_data=input_data)
        if "pg_dump" in args:
            backups += 1
            if backups == 2:
                raise RuntimeError("second backup failed")
        if backups == 2 and context == RESTORE and args[:2] == ("delete", "namespace"):
            raise KeyboardInterrupt("cancelled after failed backup")
        return result

    output = tmp_path / "suite"
    with (
        patch.object(restore, "run_kubectl", side_effect=cancelled_cleanup),
        pytest.raises(KeyboardInterrupt, match="cancelled after failed backup"),
    ):
        lab_suite.execute_lab_suite(output)
    result = json.loads((output / "suite.json").read_text())
    assert result["status"] == "interrupted"
    assert result["passed"] is False
    assert result["completedAt"] is not None
    assert [case["name"] for case in result["cases"]] == ["zero-loss", "tail-loss"]
    assert [case["passed"] for case in result["cases"]] == [True, False]
    assert [case["observedExitCode"] for case in result["cases"]] == [0, 130]
    assert result["cases"][1]["error"] == "lab interrupted"
    checks = json.loads((output / result["cases"][1]["drillRun"]).read_text())["status"]["checks"]
    assert any(check.get("detail") == "second backup failed" for check in checks)
    assert any(check.get("detail") == "cancelled after failed backup" for check in checks)
    deleted = [item[0] for item in commands if item[2][:2] == ("delete", "namespace")]
    assert deleted.count(SOURCE) == deleted.count(RESTORE) == 2
    assert not (output / "budget-loss.drillrun.json").exists()


def test_lab_suite_refuses_existing_directory_before_execution(tmp_path):
    from nostekon import lab_suite

    existing = tmp_path / "suite.json"
    existing.write_text("preserve this evidence")
    with (
        patch.object(lab_suite, "execute_isolated_drill") as execute,
        pytest.raises(FileExistsError),
    ):
        lab_suite.execute_lab_suite(tmp_path)
    execute.assert_not_called()
    assert existing.read_text() == "preserve this evidence"


@pytest.mark.parametrize(
    "parameters",
    [{"write_count": value} for value in [0, 99, True, 1.5]]
    + [{"rpo_seconds": value} for value in [0, 86401, True, 1.5]],
)
def test_lab_suite_rejects_invalid_workload_before_creating_output(tmp_path, parameters):
    from nostekon import lab_suite

    output = tmp_path / "suite"
    with (
        patch.object(lab_suite, "execute_isolated_drill") as execute,
        pytest.raises(ValueError),
    ):
        lab_suite.execute_lab_suite(output, **parameters)
    execute.assert_not_called()
    assert not output.exists()


def test_restore_only_moves_dump_to_distinct_lab_cluster(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        restore.execute_isolated_drill(output)

    evidence = json.loads(output.read_text())
    assert evidence["spec"]["upTo"] == "V4"
    assert evidence["spec"]["v4Evidence"]["invariants"] == ["probe-write-preserved"]
    assert [check["level"] for check in evidence["status"]["checks"]] == [
        "V0",
        "V1",
        "V2",
        "V3",
        "V4",
    ]
    assert evidence["status"]["checks"][3]["name"] == "recovery_probe contains exactly one row"
    assert evidence["status"]["checks"][4]["name"] == "probe-write-preserved"
    dumps = [item for item in commands if "pg_dump" in item[2]]
    restores = [item for item in commands if item[3] is not None]
    assert len(dumps) == len(restores) == 1
    assert dumps[0][0] == SOURCE
    assert restores[0][0] == RESTORE
    assert restores[0][3] == b"CREATE TABLE recovery_probe ...;"
    pods = [item for item in commands if item[2][0] == "run"]
    for _, _, args, _ in pods:
        overrides = json.loads(
            next(arg.removeprefix("--overrides=") for arg in args if arg.startswith("--overrides="))
        )
        pod = overrides["spec"]
        assert pod["automountServiceAccountToken"] is False
        container = pod["containers"][0]
        assert "listen_addresses=127.0.0.1" in container["args"]
        assert container["readinessProbe"]["exec"]["command"] == [
            "pg_isready",
            "-h",
            "127.0.0.1",
            "-U",
            "postgres",
        ]
    source_delete = next(
        index
        for index, item in enumerate(commands)
        if item[0] == SOURCE and item[2][:2] == ("delete", "namespace")
    )
    assert source_delete < commands.index(restores[0])
    assert [item[0] for item in commands if item[2][:2] == ("delete", "namespace")] == [
        SOURCE,
        RESTORE,
    ]


def test_mismatched_restored_write_is_failed_v4_evidence(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands, mismatch=True)),
        pytest.raises(RuntimeError, match="does not match"),
    ):
        restore.execute_isolated_drill(output)

    evidence = json.loads(output.read_text())
    invariant = next(
        check for check in evidence["status"]["checks"] if check["name"] == "probe-write-preserved"
    )
    assert invariant["level"] == "V4"
    assert invariant["passed"] is False
    assert len([item for item in commands if item[2][:2] == ("delete", "namespace")]) == 2


def test_lab_captures_acknowledged_writes_in_host_ledger(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        evidence = restore.execute_isolated_drill(output)

    with Ledger(output.with_name(output.name + ".ledger.db")) as ledger:
        acks = ledger.acks()
    assert len(acks) == 1
    assert acks[0].write_id == commands_write_id(commands)
    assert acks[0].acked_at <= datetime.fromisoformat(evidence["status"]["failureAt"])
    assert evidence["status"]["ledger"] == {
        "acks": [{"writeId": acks[0].write_id, "ackedAt": acks[0].acked_at.isoformat()}],
        "present": [acks[0].write_id],
    }
    assert output.with_name(output.name + ".ledger.db").stat().st_mode & 0o777 == 0o600


def test_wrong_probe_row_count_fails_v3_without_claiming_v4(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands, row_count=b"2")),
        pytest.raises(RuntimeError, match="row count does not match"),
    ):
        restore.execute_isolated_drill(output)

    evidence = json.loads(output.read_text())
    checks = evidence["status"]["checks"]
    row_count = next(
        check for check in checks if check["name"] == "recovery_probe contains exactly one row"
    )
    assert row_count["level"] == "V3" and row_count["passed"] is False
    assert evidence["spec"]["upTo"] == "V3"
    assert "v4Evidence" not in evidence["spec"]


@pytest.mark.parametrize("stage", ["insert", "query"])
def test_unmeasured_restore_never_reports_ledger_loss(tmp_path, stage):
    commands = []
    execute = fake_kubectl(commands)

    def failing(context, namespace, *args, input_data=None):
        if (stage == "insert" and any(arg.startswith("INSERT INTO") for arg in args)) or (
            stage == "query" and "SELECT write_id FROM recovery_probe" in args
        ):
            raise subprocess.CalledProcessError(1, "psql")
        return execute(context, namespace, *args, input_data=input_data)

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=failing),
        pytest.raises(subprocess.CalledProcessError),
    ):
        restore.execute_isolated_drill(output)
    evidence = json.loads(output.read_text())
    assert "ledger" not in evidence["status"]
    with Ledger(output.with_name(output.name + ".ledger.db")) as ledger:
        assert len(ledger.acks()) == (0 if stage == "insert" else 1)
    assert {item[0] for item in commands if item[2][:2] == ("delete", "namespace")} == {
        SOURCE,
        RESTORE,
    }


def test_existing_host_ledger_is_never_overwritten(tmp_path):
    output = tmp_path / "run.json"
    ledger_path = output.with_name(output.name + ".ledger.db")
    ledger_path.write_text("previous ledger")
    commands = []
    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)),
        pytest.raises(FileExistsError),
    ):
        restore.execute_isolated_drill(output)
    assert ledger_path.read_text() == "previous ledger"
    assert not output.exists()
    assert all(item[2][0] in ("get", "config") for item in commands)


@pytest.mark.parametrize("after_backup_writes", [0, 2])
def test_lab_workload_measures_acknowledged_tail_loss(tmp_path, after_backup_writes):
    from nostekon.ledger import Ack, measure_rpo

    commands = []
    output = tmp_path / "run.json"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        if after_backup_writes:
            with pytest.raises(RuntimeError, match="acknowledged-write ledger"):
                restore.execute_isolated_drill(
                    output, write_count=3, after_backup_writes=after_backup_writes
                )
        else:
            restore.execute_isolated_drill(output, write_count=3)

    evidence = json.loads(output.read_text())
    recorded = evidence["status"]["ledger"]
    acks = [Ack(datetime.fromisoformat(ack["ackedAt"]), ack["writeId"]) for ack in recorded["acks"]]
    report = measure_rpo(
        acks, recorded["present"], datetime.fromisoformat(evidence["status"]["failureAt"])
    )
    assert report.acknowledged == 3 + after_backup_writes
    assert report.recovered == 3
    assert report.lost == after_backup_writes
    assert report.holes == report.unexpected == 0
    assert evidence["spec"]["objectives"]["rpo"] == "0s"
    assert any(check["level"] == "V3" and check["passed"] for check in evidence["status"]["checks"])
    with Ledger(output.with_name(output.name + ".ledger.db")) as ledger:
        assert len(ledger.acks()) == 3 + after_backup_writes


@pytest.mark.parametrize("writes,tail", [(0, 0), (-1, 0), (101, 0), (1, -1), (99, 2)])
def test_unbounded_workload_is_rejected_before_cluster_access(tmp_path, writes, tail):
    with (
        patch.object(restore, "run_kubectl") as kubectl,
        pytest.raises(ValueError, match="write"),
    ):
        restore.execute_isolated_drill(
            tmp_path / "run.json", write_count=writes, after_backup_writes=tail
        )
    kubectl.assert_not_called()
    assert not (tmp_path / "run.json").exists()


def test_declared_rpo_budget_accepts_consistent_tail_loss(tmp_path):
    commands = []
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        evidence = restore.execute_isolated_drill(
            tmp_path / "run.json", write_count=3, after_backup_writes=2, rpo_seconds=60
        )
    assert evidence["spec"]["objectives"]["rpo"] == "60s"
    assert len(evidence["status"]["ledger"]["acks"]) == 5
    assert len(evidence["status"]["ledger"]["present"]) == 3
    assert all(check["passed"] for check in evidence["status"]["checks"])


@pytest.mark.parametrize("budget", [-1, 86401, 1.5, True, "10"])
def test_invalid_rpo_budget_is_rejected_before_cluster_access(tmp_path, budget):
    with patch.object(restore, "run_kubectl") as kubectl, pytest.raises(ValueError, match="RPO"):
        restore.execute_isolated_drill(tmp_path / "run.json", rpo_seconds=budget)
    kubectl.assert_not_called()


@pytest.mark.parametrize("replace_index,holes", [(1, 1), (2, 0)])
def test_same_row_count_cannot_hide_missing_or_unexpected_ids(tmp_path, replace_index, holes):
    from nostekon.ledger import Ack, measure_rpo

    commands = []
    execute = fake_kubectl(commands)

    def corrupt(context, namespace, *args, input_data=None):
        result = execute(context, namespace, *args, input_data=input_data)
        if "SELECT write_id FROM recovery_probe" in args:
            identifiers = result.decode().splitlines()
            identifiers[replace_index] = "unacknowledged-write"
            return "\n".join(identifiers).encode()
        return result

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=corrupt),
        pytest.raises(RuntimeError, match="acknowledged-write ledger"),
    ):
        restore.execute_isolated_drill(output, write_count=3, rpo_seconds=60)
    evidence = json.loads(output.read_text())
    status = evidence["status"]
    recorded = status["ledger"]
    acks = [Ack(datetime.fromisoformat(ack["ackedAt"]), ack["writeId"]) for ack in recorded["acks"]]
    report = measure_rpo(acks, recorded["present"], datetime.fromisoformat(status["failureAt"]))
    assert (report.lost, report.holes, report.unexpected) == (1, holes, 1)
    assert any(check["level"] == "V3" and check["passed"] for check in status["checks"])
    assert any(check["level"] == "V4" and not check["passed"] for check in status["checks"])


@pytest.mark.parametrize("malformed", ["duplicate", "blank", "truncated"])
def test_malformed_restored_ids_are_not_published_as_measured_ledger(tmp_path, malformed):
    commands = []
    execute = fake_kubectl(commands)

    def invalid(context, namespace, *args, input_data=None):
        result = execute(context, namespace, *args, input_data=input_data)
        if "SELECT write_id FROM recovery_probe" in args:
            identifiers = result.decode().splitlines()
            if malformed == "duplicate":
                identifiers[1] = identifiers[0]
            elif malformed == "blank":
                identifiers[1] = ""
            else:
                identifiers.pop()
            return "\n".join(identifiers).encode()
        return result

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=invalid),
        pytest.raises(RuntimeError, match="restored write IDs"),
    ):
        restore.execute_isolated_drill(output, write_count=3, rpo_seconds=60)
    status = json.loads(output.read_text())["status"]
    assert "ledger" not in status
    assert any(check["level"] == "V4" and not check["passed"] for check in status["checks"])
    assert {item[0] for item in commands if item[2][:2] == ("delete", "namespace")} == {
        SOURCE,
        RESTORE,
    }


def test_same_cluster_contexts_are_rejected_before_mutation(tmp_path):
    commands = []

    def same_cluster(context, namespace, *args, input_data=None):
        commands.append(args)
        if args[:2] == ("config", "view"):
            return b"https://127.0.0.1:6443"
        return b"same-uid"

    with (
        patch.object(restore, "run_kubectl", side_effect=same_cluster),
        pytest.raises(RuntimeError, match="distinct clusters"),
    ):
        restore.execute_isolated_drill(tmp_path / "run.json")
    assert all(args[0] in ("get", "config") for args in commands)


def test_lab_uses_nostekon_contexts_and_namespaces(tmp_path):
    assert (SOURCE, RESTORE) == ("k3d-nostekon-source", "k3d-nostekon-restore")
    for stale in ("k3d-checkride-source", "kind-production"):
        with (
            patch.object(restore, "run_kubectl") as kubectl,
            pytest.raises(RuntimeError, match=r"k3d-nostekon-\*"),
        ):
            restore.execute_isolated_drill(tmp_path / f"{stale}.json", source_context=stale)
        kubectl.assert_not_called()
    commands = []
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        evidence = restore.execute_isolated_drill(tmp_path / "run.json")
    assert evidence["metadata"]["name"].startswith("nostekon-")


def test_command_failure_writes_failed_evidence_and_cleans_up(tmp_path):
    commands = []
    execute = fake_kubectl(commands)

    def failing(context, namespace, *args, input_data=None):
        if "pg_dump" in args:
            raise subprocess.CalledProcessError(1, "pg_dump")
        return execute(context, namespace, *args, input_data=input_data)

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=failing),
        pytest.raises(subprocess.CalledProcessError),
    ):
        restore.execute_isolated_drill(output)
    status = json.loads(output.read_text())["status"]
    assert status["checks"][-1]["passed"] is False
    assert datetime.fromisoformat(status["failureAt"]) <= datetime.fromisoformat(
        status["completedAt"]
    )
    assert len([item for item in commands if item[2][:2] == ("delete", "namespace")]) == 2


def test_cleanup_failure_cannot_leave_verified_report(tmp_path):
    commands = []
    execute = fake_kubectl(commands)

    def failing_cleanup(context, namespace, *args, input_data=None):
        if context == RESTORE and args[:2] == ("delete", "namespace"):
            raise subprocess.CalledProcessError(1, "kubectl delete", stderr=b"timeout")
        return execute(context, namespace, *args, input_data=input_data)

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=failing_cleanup),
        pytest.raises(subprocess.CalledProcessError),
    ):
        restore.execute_isolated_drill(output)
    checks = json.loads(output.read_text())["status"]["checks"]
    assert any(not check["passed"] for check in checks)


def test_cleanup_interrupt_overrides_prior_execution_error(tmp_path):
    commands = []
    execute = fake_kubectl(commands)

    def interrupted(context, namespace, *args, input_data=None):
        result = execute(context, namespace, *args, input_data=input_data)
        if "pg_dump" in args:
            raise RuntimeError("backup failed before cancellation")
        if context == RESTORE and args[:2] == ("delete", "namespace"):
            raise KeyboardInterrupt("operator cancelled cleanup")
        return result

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=interrupted),
        pytest.raises(KeyboardInterrupt, match="operator cancelled cleanup"),
    ):
        restore.execute_isolated_drill(output)
    checks = json.loads(output.read_text())["status"]["checks"]
    assert any(check.get("detail") == "backup failed before cancellation" for check in checks)
    assert any(check.get("detail") == "operator cancelled cleanup" for check in checks)
    deleted = {item[0] for item in commands if item[2][:2] == ("delete", "namespace")}
    assert deleted == {SOURCE, RESTORE}


def test_check_cluster_health_evaluates_node_readiness():
    from nostekon.lab import check_cluster_health

    def mock_kubectl(context, namespace, *args, input_data=None):
        if "kube-system" in args:
            return b"system-uid"
        if "get" in args and "nodes" in args:
            return b"True True False"
        return b""

    with patch("nostekon.lab.run_kubectl", side_effect=mock_kubectl):
        res = check_cluster_health("test-ctx")
    assert res["reachable"] is True
    assert res["uid"] == "system-uid"
    assert res["ready_nodes"] == 2


@pytest.mark.parametrize("stage", ["pg_dump", "cleanup"])
def test_timeout_records_failure_and_attempts_all_cleanup(tmp_path, stage):
    commands = []
    execute = fake_kubectl(commands)

    def timeout(context, namespace, *args, input_data=None):
        result = execute(context, namespace, *args, input_data=input_data)
        if (stage == "pg_dump" and "pg_dump" in args) or (
            stage == "cleanup" and args[:2] == ("delete", "namespace")
        ):
            raise subprocess.TimeoutExpired("kubectl", 210)
        return result

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=timeout),
        pytest.raises(subprocess.TimeoutExpired),
    ):
        restore.execute_isolated_drill(output)
    status = json.loads(output.read_text())["status"]
    assert any(not check["passed"] for check in status["checks"])
    deleted = {item[0] for item in commands if item[2][:2] == ("delete", "namespace")}
    assert deleted == {SOURCE, RESTORE}


def test_remote_endpoint_is_rejected_before_mutation(tmp_path):
    with (
        patch.object(
            restore, "run_kubectl", return_value=b"https://cluster.example.com:6443"
        ) as kubectl,
        pytest.raises(RuntimeError, match="local"),
    ):
        restore.execute_isolated_drill(tmp_path / "run.json")
    assert all(call.args[2] == "config" for call in kubectl.call_args_list)


def test_existing_evidence_is_never_overwritten(tmp_path):
    output = tmp_path / "run.json"
    output.write_text("previous evidence")
    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl([])),
        pytest.raises(FileExistsError),
    ):
        restore.execute_isolated_drill(output)
    assert output.read_text() == "previous evidence"


def test_kubectl_has_a_process_deadline_and_local_proxy_bypass(monkeypatch):
    monkeypatch.setenv("NO_PROXY", "example.com")
    with patch.object(restore.subprocess, "run") as execute:
        restore.run_kubectl(SOURCE, None, "get", "nodes")
    assert execute.call_args.kwargs["timeout"] == 210
    assert {"localhost", "127.0.0.1", "0.0.0.0", "example.com"} <= set(
        execute.call_args.kwargs["env"]["NO_PROXY"].split(",")
    )
