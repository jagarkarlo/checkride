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
