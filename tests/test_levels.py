import pytest

from nostekon.levels import LEVELS, Level, deepest_passed, first_failed, parse_level


@pytest.mark.parametrize("value", ["V3", "v3", " V3 ", "3", 3, Level.V3])
def test_parse_level_accepts_common_spellings(value: str | int) -> None:
    assert parse_level(value) is Level.V3


@pytest.mark.parametrize("value", ["V5", "-1", "L3", "", "V"])
def test_parse_level_rejects_unknown_levels(value: str) -> None:
    with pytest.raises(ValueError, match="unknown verification level"):
        parse_level(value)


def test_every_level_is_described() -> None:
    assert set(LEVELS) == set(Level)


def test_first_failed_is_the_lowest_failing_level() -> None:
    results = {Level.V0: True, Level.V1: True, Level.V2: False, Level.V3: True, Level.V4: False}
    assert first_failed(results) is Level.V2


def test_first_failed_is_none_when_everything_passed() -> None:
    assert first_failed(dict.fromkeys(Level, True)) is None


def test_deepest_passed_requires_a_contiguous_chain() -> None:
    results = {Level.V0: True, Level.V1: True, Level.V3: True}
    assert deepest_passed(results) is Level.V1


def test_deepest_passed_stops_at_the_first_failure() -> None:
    results = {Level.V0: True, Level.V1: False, Level.V2: True}
    assert deepest_passed(results) is Level.V0


def test_deepest_passed_is_none_without_v0() -> None:
    assert deepest_passed({}) is None
    assert deepest_passed({Level.V0: False}) is None


def test_deepest_passed_reaches_v4_when_all_levels_pass() -> None:
    assert deepest_passed(dict.fromkeys(Level, True)) is Level.V4
