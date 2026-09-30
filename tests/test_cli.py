import json
from pathlib import Path

import pytest

from checkride import __version__
from checkride.cli import main

EXAMPLES = Path(__file__).parent.parent / "examples" / "drills"


def test_version_command_prints_version(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["version"]) == 0
    assert capsys.readouterr().out.strip() == __version__


def test_a_command_is_required() -> None:
    with pytest.raises(SystemExit) as exit_info:
        main([])
    assert exit_info.value.code == 2


def test_levels_command_lists_all_levels(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["levels"]) == 0
    lines = capsys.readouterr().out.splitlines()
    assert [line.split()[0] for line in lines] == ["V0", "V1", "V2", "V3", "V4"]


def test_validate_accepts_the_examples(capsys: pytest.CaptureFixture[str]) -> None:
    files = sorted(str(path) for path in EXAMPLES.glob("*.yaml"))
    assert main(["validate", *files]) == 0
    assert capsys.readouterr().out.count("ok    ") == len(files)


def test_validate_reports_errors(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    broken = tmp_path / "broken.yaml"
    broken.write_text("apiVersion: checkride/v1alpha1\nkind: Drill\n")
    missing = tmp_path / "missing.yaml"
    assert main(["validate", str(broken), str(missing)]) == 1
    output = capsys.readouterr().out
    assert f"error {broken}: metadata: Field required" in output
    assert f"error {missing}: No such file or directory" in output


def test_validate_reports_invalid_yaml(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    broken = tmp_path / "broken.yaml"
    broken.write_text("spec: [unclosed\n")
    assert main(["validate", str(broken)]) == 1
    assert "invalid YAML" in capsys.readouterr().out


def test_schema_command_prints_json(capsys: pytest.CaptureFixture[str]) -> None:
    assert main(["schema"]) == 0
    assert json.loads(capsys.readouterr().out)["title"] == "DrillSpec"


def write_acks(tmp_path: Path) -> Path:
    csv_file = tmp_path / "acks.csv"
    csv_file.write_text(
        "# write_id,acked_at\n"
        "w1,2026-10-01T10:00:00Z\n"
        "w2,2026-10-01T10:00:01Z\n"
        "w3,2026-10-01T10:00:02+00:00\n"
    )
    ledger = tmp_path / "run.ledger.db"
    assert main(["import-acks", "--ledger", str(ledger), str(csv_file)]) == 0
    return ledger


def test_rpo_command_reports_tail_loss(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    ledger = write_acks(tmp_path)
    present = tmp_path / "present.txt"
    present.write_text("w1\nw2\n")
    capsys.readouterr()
    command = ["rpo", "--ledger", str(ledger), "--present", str(present)]
    assert main([*command, "--failure-at", "2026-10-01T10:00:03Z", "--json"]) == 0
    report = json.loads(capsys.readouterr().out)
    assert (report["lost"], report["holes"], report["rpoSeconds"]) == (1, 0, 2.0)


def test_rpo_command_fails_on_holes(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    ledger = write_acks(tmp_path)
    present = tmp_path / "present.txt"
    present.write_text("w1\nw3\n")
    command = ["rpo", "--ledger", str(ledger), "--present", str(present)]
    assert main([*command, "--failure-at", "2026-10-01T10:00:03Z"]) == 1
    assert "holes           1" in capsys.readouterr().out


def test_rpo_command_needs_an_existing_ledger(tmp_path: Path) -> None:
    missing = str(tmp_path / "missing.db")
    command = ["rpo", "--ledger", missing, "--present", "-", "--failure-at", "2026-10-01T10:00Z"]
    assert main(command) == 2


def test_rpo_command_rejects_naive_failure_time(tmp_path: Path) -> None:
    command = ["rpo", "--ledger", "x.db", "--present", "-", "--failure-at", "2026-10-01T10:00"]
    with pytest.raises(SystemExit) as exit_info:
        main(command)
    assert exit_info.value.code == 2


def test_import_acks_reports_bad_rows(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    csv_file = tmp_path / "acks.csv"
    csv_file.write_text("w1,2026-10-01T10:00:00Z\nw2,yesterday\n")
    assert main(["import-acks", "--ledger", str(tmp_path / "run.ledger.db"), str(csv_file)]) == 1
    assert f"error {csv_file}:2: invalid ISO 8601 timestamp" in capsys.readouterr().err


def test_import_acks_rejects_duplicates(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    csv_file = tmp_path / "acks.csv"
    csv_file.write_text("w1,2026-10-01T10:00:00Z\nw1,2026-10-01T10:00:01Z\n")
    assert main(["import-acks", "--ledger", str(tmp_path / "run.ledger.db"), str(csv_file)]) == 1
    assert "duplicate write id 'w1'" in capsys.readouterr().err


def test_lab_command_requires_subcommand() -> None:
    with pytest.raises(SystemExit) as exit_info:
        main(["lab"])
    assert exit_info.value.code == 2


def test_lab_run_invokes_execute_isolated_drill(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from checkride import lab

    called_kwargs: dict = {}

    def fake_execute(**kwargs):
        called_kwargs.update(kwargs)
        return {"status": "ok"}

    monkeypatch.setattr(lab, "execute_isolated_drill", fake_execute)
    output = tmp_path / "out.json"
    assert main(["lab", "run", "--output", str(output)]) == 0
    assert called_kwargs["output"] == output
    assert called_kwargs["source_context"] == lab.DEFAULT_SOURCE_CONTEXT
    assert called_kwargs["restore_context"] == lab.DEFAULT_RESTORE_CONTEXT


def test_lab_run_returns_error_on_exception(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    from checkride import lab

    def failing_execute(**kwargs):
        raise RuntimeError("cluster unreachable")

    monkeypatch.setattr(lab, "execute_isolated_drill", failing_execute)
    output = tmp_path / "out.json"
    assert main(["lab", "run", "--output", str(output)]) == 1
    assert "error: lab restore drill failed: cluster unreachable" in capsys.readouterr().err


def test_lab_status_reports_ready_and_not_ready(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    from checkride import lab

    def fake_health(context: str):
        if "source" in context:
            return {"context": context, "reachable": True, "uid": "123", "ready_nodes": 2}
        return {
            "context": context,
            "reachable": False,
            "error": "connection refused",
            "ready_nodes": 0,
        }

    monkeypatch.setattr(lab, "check_cluster_health", fake_health)
    assert main(["lab", "status"]) == 1
    output = capsys.readouterr().out
    assert "source   [READY    ]" in output
    assert "restore  [NOT READY]" in output
