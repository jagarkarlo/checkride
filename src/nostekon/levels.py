"""Verification levels V0-V4: how deeply a drill checks the restored application."""

from collections.abc import Mapping
from dataclasses import dataclass
from enum import IntEnum


class Level(IntEnum):
    V0 = 0
    V1 = 1
    V2 = 2
    V3 = 3
    V4 = 4


@dataclass(frozen=True)
class LevelInfo:
    question: str
    evidence: str


LEVELS: Mapping[Level, LevelInfo] = {
    Level.V0: LevelInfo("Did the backup report success?", "Backup tool status"),
    Level.V1: LevelInfo("Did the restore report success?", "Restore tool status"),
    Level.V2: LevelInfo("Is the workload healthy?", "Pods ready, HTTP and TCP checks"),
    Level.V3: LevelInfo("Is the data structurally intact?", "Tables, row counts, checksums"),
    Level.V4: LevelInfo(
        "Is the data correct?",
        "Business invariants, acknowledged-write ledger, cross-store consistency",
    ),
}


def parse_level(value: str | int) -> Level:
    """Accept V3, v3, "3" or 3."""
    if isinstance(value, Level):
        return value
    text = str(value).strip().upper().removeprefix("V")
    if text.isdigit():
        try:
            return Level(int(text))
        except ValueError:
            pass
    raise ValueError(f"unknown verification level {value!r}; expected V0 to V4")


def first_failed(results: Mapping[Level, bool]) -> Level | None:
    """Return the lowest checked level that failed, i.e. where a fault was first detected."""
    failed = [level for level, passed in results.items() if not passed]
    return min(failed) if failed else None


def deepest_passed(results: Mapping[Level, bool]) -> Level | None:
    """Return the highest level reached with every lower level checked and passed."""
    deepest = None
    for level in Level:
        if not results.get(level, False):
            break
        deepest = level
    return deepest
