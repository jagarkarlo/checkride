"""Acknowledged-write ledger: ground truth for measuring exact RPO after a restore."""

import sqlite3
from collections.abc import Iterable
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path
from types import TracebackType
from typing import Self

_EPOCH = datetime(1970, 1, 1, tzinfo=UTC)


def _to_micros(moment: datetime) -> int:
    if moment.utcoffset() is None:
        raise ValueError(f"timestamp {moment.isoformat()} has no UTC offset")
    return (moment - _EPOCH) // timedelta(microseconds=1)


def _from_micros(micros: int) -> datetime:
    return _EPOCH + timedelta(microseconds=micros)


@dataclass(frozen=True, order=True)
class Ack:
    acked_at: datetime
    write_id: str


class Ledger:
    """Append-only SQLite record of writes the application saw acknowledged.

    Keep the ledger outside the clusters under test, so a lost cluster cannot take the
    ground truth with it.
    """

    def __init__(self, path: str | Path) -> None:
        self._db = sqlite3.connect(path)
        self._db.execute(
            "CREATE TABLE IF NOT EXISTS acks "
            "(write_id TEXT PRIMARY KEY, acked_at_us INTEGER NOT NULL)"
        )

    def record(self, write_id: str, acked_at: datetime) -> None:
        with self._db:
            self._db.execute("INSERT INTO acks VALUES (?, ?)", (write_id, _to_micros(acked_at)))

    def acks(self) -> list[Ack]:
        rows = self._db.execute(
            "SELECT acked_at_us, write_id FROM acks ORDER BY acked_at_us, write_id"
        )
        return [Ack(_from_micros(micros), write_id) for micros, write_id in rows]

    def close(self) -> None:
        self._db.close()

    def __enter__(self) -> Self:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()


@dataclass(frozen=True)
class RpoReport:
    failure_at: datetime
    acknowledged: int
    recovered: int
    lost: int
    holes: int
    unexpected: int
    recovery_point: datetime | None
    first_lost_at: datetime | None
    rpo: timedelta

    @property
    def consistent(self) -> bool:
        return self.holes == 0

    @property
    def resolution(self) -> timedelta | None:
        """Gap between the last recovered and the first lost write; the true RPO lies within."""
        if self.recovery_point is None or self.first_lost_at is None:
            return None
        return self.first_lost_at - self.recovery_point


def measure_rpo(acks: Iterable[Ack], present: Iterable[str], failure_at: datetime) -> RpoReport:
    """Compare acknowledged writes with the write IDs found in the restored database.

    Only writes acknowledged at or before ``failure_at`` count. The recovery point is the
    last write of the longest fully recovered prefix, and ``rpo`` is ``failure_at`` minus
    that point, or minus the first acknowledgement if nothing was recovered. Holes are lost
    writes older than a recovered one, which a consistent point-in-time restore never
    produces. Unexpected IDs were restored but never acknowledged before the failure.
    """
    _to_micros(failure_at)
    expected = sorted(ack for ack in acks if ack.acked_at <= failure_at)
    found = set(present)
    recovered = [ack.write_id in found for ack in expected]

    prefix = next((index for index, ok in enumerate(recovered) if not ok), len(expected))
    lost = [index for index, ok in enumerate(recovered) if not ok]
    last_recovered = max((index for index, ok in enumerate(recovered) if ok), default=-1)
    recovery_point = expected[prefix - 1].acked_at if prefix else None

    if not lost:
        rpo = timedelta(0)
    elif recovery_point is not None:
        rpo = failure_at - recovery_point
    else:
        rpo = failure_at - expected[0].acked_at

    return RpoReport(
        failure_at=failure_at,
        acknowledged=len(expected),
        recovered=len(expected) - len(lost),
        lost=len(lost),
        holes=sum(1 for index in lost if index < last_recovered),
        unexpected=len(found - {ack.write_id for ack in expected}),
        recovery_point=recovery_point,
        first_lost_at=expected[lost[0]].acked_at if lost else None,
        rpo=rpo,
    )
