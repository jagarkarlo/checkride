"""Command-boundary tests for the disposable k3d restore runner."""

import importlib.util
import json
import subprocess
from pathlib import Path
from unittest.mock import patch

import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/lab/restore.py"
SPEC = importlib.util.spec_from_file_location("lab_restore", SCRIPT)
restore = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(restore)


def fake_kubectl(commands, mismatch=False):
    def execute(context, namespace, *args, input_data=None):
        commands.append((context, namespace, args, input_data))
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
    with patch.object(restore, "kubectl", side_effect=fake_kubectl(commands)):
        restore.run(output)

    evidence = json.loads(output.read_text())
    assert [check["level"] for check in evidence["status"]["checks"]] == ["V0", "V1", "V2", "V3"]
    dumps = [item for item in commands if "pg_dump" in item[2]]
    restores = [item for item in commands if item[3] is not None]
    assert len(dumps) == len(restores) == 1
    assert dumps[0][0] == restore.SOURCE
    assert restores[0][0] == restore.RESTORE
    assert restores[0][3] == b"CREATE TABLE recovery_probe ...;"
    source_delete = next(
        index
        for index, item in enumerate(commands)
        if item[0] == restore.SOURCE and item[2][:2] == ("delete", "namespace")
    )
    assert source_delete < commands.index(restores[0])
    assert [item[0] for item in commands if item[2][:2] == ("delete", "namespace")] == [
        restore.SOURCE,
        restore.RESTORE,
    ]


def test_mismatched_restored_write_is_failed_v3_evidence(tmp_path):
    commands = []
    output = tmp_path / "run.json"
    with (
        patch.object(restore, "kubectl", side_effect=fake_kubectl(commands, mismatch=True)),
        pytest.raises(RuntimeError, match="does not match"),
    ):
        restore.run(output)

    evidence = json.loads(output.read_text())
    assert evidence["status"]["checks"][-1]["level"] == "V3"
    assert evidence["status"]["checks"][-1]["passed"] is False
    assert len([item for item in commands if item[2][:2] == ("delete", "namespace")]) == 2


def test_same_cluster_contexts_are_rejected_before_mutation(tmp_path):
    commands = []

    def same_cluster(context, namespace, *args, input_data=None):
        commands.append(args)
        return b"same-uid"

    with (
        patch.object(restore, "kubectl", side_effect=same_cluster),
        pytest.raises(RuntimeError, match="distinct clusters"),
    ):
        restore.run(tmp_path / "run.json")
    assert len(commands) == 2


def test_command_failure_writes_failed_evidence_and_cleans_up(tmp_path):
    commands = []
    execute = fake_kubectl(commands)

    def failing(context, namespace, *args, input_data=None):
        if "pg_dump" in args:
            raise subprocess.CalledProcessError(1, "pg_dump")
        return execute(context, namespace, *args, input_data=input_data)

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "kubectl", side_effect=failing),
        pytest.raises(subprocess.CalledProcessError),
    ):
        restore.run(output)
    assert json.loads(output.read_text())["status"]["checks"][-1]["passed"] is False
    assert len([item for item in commands if item[2][:2] == ("delete", "namespace")]) == 2


def test_cleanup_failure_cannot_leave_verified_report(tmp_path):
    commands = []
    execute = fake_kubectl(commands)

    def failing_cleanup(context, namespace, *args, input_data=None):
        if context == restore.RESTORE and args[:2] == ("delete", "namespace"):
            raise subprocess.CalledProcessError(1, "kubectl delete", stderr=b"timeout")
        return execute(context, namespace, *args, input_data=input_data)

    output = tmp_path / "run.json"
    with (
        patch.object(restore, "kubectl", side_effect=failing_cleanup),
        pytest.raises(subprocess.CalledProcessError),
    ):
        restore.run(output)
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
