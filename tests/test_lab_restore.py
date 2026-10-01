"""Command-boundary tests for the disposable k3d restore runner."""

import json
import subprocess
from datetime import datetime
from unittest.mock import patch

import pytest

from checkride import lab as restore

SOURCE = restore.DEFAULT_SOURCE_CONTEXT
RESTORE = restore.DEFAULT_RESTORE_CONTEXT


def fake_kubectl(commands, mismatch=False):
    def execute(context, namespace, *args, input_data=None):
        commands.append((context, namespace, args, input_data))
        if args[:2] == ("config", "view"):
            return b"https://127.0.0.1:6443"
        if args[:2] == ("get", "namespace"):
            return context.encode()
        if "pg_dump" in args:
            return b"CREATE TABLE recovery_probe ...;"
        if "SELECT write_id FROM recovery_probe" in args:
            return b"wrong" if mismatch else commands_write_id(commands).encode()
        return b""

    return execute


def commands_write_id(commands):
    insert = next(
        arg for _, _, args, _ in commands for arg in args if arg.startswith("INSERT INTO")
    )
    return insert.split("'")[1]


def test_restore_only_moves_dump_to_distinct_lab_cluster(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands)):
        restore.execute_isolated_drill(output)

    evidence = json.loads(output.read_text())
    assert [check["level"] for check in evidence["status"]["checks"]] == ["V0", "V1", "V2", "V3"]
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


def test_mismatched_restored_write_is_failed_v3_evidence(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with (
        patch.object(restore, "run_kubectl", side_effect=fake_kubectl(commands, mismatch=True)),
        pytest.raises(RuntimeError, match="does not match"),
    ):
        restore.execute_isolated_drill(output)

    evidence = json.loads(output.read_text())
    assert evidence["status"]["checks"][-1]["level"] == "V3"
    assert evidence["status"]["checks"][-1]["passed"] is False
    assert len([item for item in commands if item[2][:2] == ("delete", "namespace")]) == 2


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
