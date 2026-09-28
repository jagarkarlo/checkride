import json
from datetime import datetime, timedelta
from pathlib import Path

import pytest

from checkride.ledger import Ack, measure_rpo

CASES = json.loads((Path(__file__).parent / "contracts" / "rpo_cases.json").read_text())["cases"]


def _time(value: str | None) -> datetime | None:
    return None if value is None else datetime.fromisoformat(value)


@pytest.mark.parametrize("case", CASES, ids=[case["name"] for case in CASES])
def test_measure_rpo_matches_shared_contract(case: dict) -> None:
    acks = [Ack(datetime.fromisoformat(at), write_id) for write_id, at in case["acks"]]
    report = measure_rpo(acks, case["present"], datetime.fromisoformat(case["failureAt"]))
    expected = case["expected"]
    assert (
        report.acknowledged,
        report.recovered,
        report.lost,
        report.holes,
        report.unexpected,
    ) == (
        expected["acknowledged"],
        expected["recovered"],
        expected["lost"],
        expected["holes"],
        expected["unexpected"],
    )
    assert report.recovery_point == _time(expected["recoveryPoint"])
    assert report.first_lost_at == _time(expected["firstLostAt"])
    assert report.rpo == timedelta(seconds=expected["rpoSeconds"])
