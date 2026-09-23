import pytest

from checkride import __version__
from checkride.cli import main


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
