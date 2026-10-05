"""Command-boundary tests for the disposable k3d restore runner."""

import json
import subprocess
from datetime import datetime
from unittest.mock import patch

import pytest

from checkride import lab as restore
from checkride.ledger import Ledger

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


def test_lab_suite_measures_all_three_policy_outcomes(tmp_path):
    from checkride.lab_suite import execute_lab_suite

    commands = []
    output = tmp_path / "suite"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        result = execute_lab_suite(output, write_count=3)
    assert result["passed"] is True
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
        assert (output / case["ledger"]).is_file()


@pytest.mark.parametrize("fault", ["restore-error", "cleanup-error", "unexpected-pass"])
def test_lab_suite_never_accepts_unrelated_failure_as_expected_tail_loss(tmp_path, fault):
    from checkride import lab_suite

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
    from checkride import lab_suite

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


def test_lab_suite_refuses_existing_directory_before_execution(tmp_path):
    from checkride import lab_suite

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
    from checkride import lab_suite

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
    from checkride.ledger import Ack, measure_rpo

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
    from checkride.ledger import Ack, measure_rpo

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


def test_check_cluster_health_evaluates_node_readiness():
    from checkride.lab import check_cluster_health

    def mock_kubectl(context, namespace, *args, input_data=None):
        if "kube-system" in args:
            return b"system-uid"
        if "get" in args and "nodes" in args:
            return b"True True False"
        return b""

    with patch("checkride.lab.run_kubectl", side_effect=mock_kubectl):
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
