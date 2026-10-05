import sqlite3
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from nostekon.ledger import Ack, Ledger, measure_rpo

START = datetime(2026, 10, 1, 10, 0, tzinfo=UTC)


def acks_every_second(count: int) -> list[Ack]:
    return [Ack(START + timedelta(seconds=index), f"w{index:03d}") for index in range(count)]


def ids(acks: list[Ack]) -> list[str]:
    return [ack.write_id for ack in acks]


def test_nothing_lost_means_zero_rpo() -> None:
    acks = acks_every_second(10)
    report = measure_rpo(acks, ids(acks), failure_at=START + timedelta(seconds=10))
    assert (report.acknowledged, report.recovered, report.lost) == (10, 10, 0)
    assert report.rpo == timedelta(0)
    assert report.consistent
    assert report.first_lost_at is None


def test_tail_loss_measures_rpo_from_the_recovery_point() -> None:
    acks = acks_every_second(10)
    failure_at = START + timedelta(seconds=9, milliseconds=500)
    report = measure_rpo(acks, ids(acks[:7]), failure_at)
    assert (report.recovered, report.lost, report.holes) == (7, 3, 0)
    assert report.recovery_point == acks[6].acked_at
    assert report.first_lost_at == acks[7].acked_at
    assert report.rpo == timedelta(seconds=3, milliseconds=500)
    assert report.resolution == timedelta(seconds=1)


def test_holes_mark_an_inconsistent_restore() -> None:
    acks = acks_every_second(6)
    present = ids(acks[:2]) + ids(acks[4:])
    report = measure_rpo(acks, present, failure_at=START + timedelta(seconds=6))
    assert (report.lost, report.holes) == (2, 2)
    assert not report.consistent
    assert report.recovery_point == acks[1].acked_at


def test_writes_acknowledged_after_the_failure_are_ignored() -> None:
    acks = acks_every_second(5)
    failure_at = START + timedelta(seconds=2)
    report = measure_rpo(acks, ids(acks), failure_at)
    assert (report.acknowledged, report.lost) == (3, 0)
    assert report.unexpected == 2


def test_unknown_ids_are_unexpected() -> None:
    acks = acks_every_second(3)
    report = measure_rpo(acks, [*ids(acks), "ghost"], failure_at=START + timedelta(seconds=3))
    assert report.unexpected == 1


def test_total_loss_counts_from_the_first_acknowledgement() -> None:
    acks = acks_every_second(4)
    failure_at = START + timedelta(seconds=5)
    report = measure_rpo(acks, [], failure_at)
    assert report.recovery_point is None
    assert report.rpo == timedelta(seconds=5)
    assert report.resolution is None


def test_failure_time_needs_an_offset() -> None:
    with pytest.raises(ValueError, match="no UTC offset"):
        measure_rpo([], [], datetime(2026, 10, 1, 10, 0))


def test_ledger_round_trips_in_acknowledgement_order(tmp_path: Path) -> None:
    path = tmp_path / "run.ledger.db"
    with Ledger(path) as ledger:
        ledger.record("b", START + timedelta(microseconds=2))
        ledger.record("a", START + timedelta(microseconds=1))
    with Ledger(path) as ledger:
        assert ledger.acks() == [
            Ack(START + timedelta(microseconds=1), "a"),
            Ack(START + timedelta(microseconds=2), "b"),
        ]


def test_ledger_rejects_duplicate_write_ids(tmp_path: Path) -> None:
    with Ledger(tmp_path / "run.ledger.db") as ledger:
        ledger.record("a", START)
        with pytest.raises(sqlite3.IntegrityError):
            ledger.record("a", START)


def test_ledger_rejects_naive_timestamps(tmp_path: Path) -> None:
    with Ledger(tmp_path / "run.ledger.db") as ledger, pytest.raises(ValueError):
        ledger.record("a", datetime(2026, 10, 1, 10, 0))
