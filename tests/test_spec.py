from datetime import timedelta
from pathlib import Path
from typing import Any

import pytest
import yaml
from pydantic import ValidationError

from nostekon.levels import Level
from nostekon.spec import (
    RestoreInto,
    Scenario,
    describe_errors,
    drill_schema,
    format_duration,
    lint,
    load_drill,
    parse_duration,
)

EXAMPLES = Path(__file__).parent.parent / "examples" / "drills"


def minimal_drill(**spec: Any) -> dict[str, Any]:
    body: dict[str, Any] = {
        "scenario": "namespace-loss",
        "target": {"namespace": "shop", "cnpgCluster": "shop-db"},
        "verify": {"upTo": "V4", "ledger": True},
        "objectives": {"rto": "15m", "rpo": "5m"},
    }
    body.update(spec)
    return {
        "apiVersion": "nostekon/v1alpha1",
        "kind": "Drill",
        "metadata": {"name": "demo"},
        "spec": body,
    }


def write_drill(tmp_path: Path, document: dict[str, Any] | str) -> Path:
    path = tmp_path / "drill.yaml"
    path.write_text(document if isinstance(document, str) else yaml.safe_dump(document))
    return path


def errors_of(tmp_path: Path, document: dict[str, Any] | str) -> list[str]:
    with pytest.raises(ValidationError) as error:
        load_drill(write_drill(tmp_path, document))
    return describe_errors(error.value)


@pytest.mark.parametrize(
    ("text", "expected"),
    [("90s", 90), ("15m", 900), ("1h30m", 5400), ("2h", 7200), ("0s", 0), (" 5m ", 300), (45, 45)],
)
def test_parse_duration(text: str | int, expected: int) -> None:
    assert parse_duration(text) == timedelta(seconds=expected)


@pytest.mark.parametrize("text", ["", "15", "1d", "m", "-5m", "5 m", True])
def test_parse_duration_rejects_invalid_values(text: str) -> None:
    with pytest.raises(ValueError, match="invalid duration"):
        parse_duration(text)


@pytest.mark.parametrize("seconds", [0, 45, 900, 5400, 3661])
def test_format_duration_round_trips(seconds: int) -> None:
    value = timedelta(seconds=seconds)
    assert parse_duration(format_duration(value)) == value


@pytest.mark.parametrize("path", sorted(EXAMPLES.glob("*.yaml")), ids=lambda path: path.name)
def test_examples_are_valid(path: Path) -> None:
    load_drill(path)


def test_example_fields_are_parsed() -> None:
    drill = load_drill(EXAMPLES / "mlflow-namespace-loss.yaml")
    assert drill.spec.scenario is Scenario.NAMESPACE_LOSS
    assert drill.spec.restore.into is RestoreInto.SEPARATE_CLUSTER
    assert drill.spec.verify.up_to is Level.V4
    assert drill.spec.objectives.rto == timedelta(minutes=15)
    assert drill.spec.verify.invariants[0].expect == 0


@pytest.mark.parametrize("timestamp", ["'2026-10-01T12:00:00'", "2026-10-01T12:00:00"])
def test_point_in_time_without_offset_is_rejected(tmp_path: Path, timestamp: str) -> None:
    document = yaml.safe_dump(minimal_drill(restore={"pointInTime": "PLACEHOLDER"}))
    document = document.replace("PLACEHOLDER", timestamp)
    errors = errors_of(tmp_path, document)
    assert any("needs a UTC offset" in error for error in errors)


def test_point_in_time_keeps_its_offset(tmp_path: Path) -> None:
    document = minimal_drill(restore={"pointInTime": "2026-10-01T12:00:00+02:00"})
    drill = load_drill(write_drill(tmp_path, document))
    assert drill.spec.restore.point_in_time.utcoffset() == timedelta(hours=2)


def test_cluster_loss_cannot_restore_into_a_namespace(tmp_path: Path) -> None:
    document = minimal_drill(scenario="cluster-loss", restore={"into": "namespace"})
    assert errors_of(tmp_path, document) == [
        "spec: a cluster-loss drill must restore into a separate cluster"
    ]


def test_v4_needs_ledger_or_invariants(tmp_path: Path) -> None:
    document = minimal_drill(verify={"upTo": "V4"})
    assert errors_of(tmp_path, document) == [
        "spec.verify: V4 verification needs the write ledger or at least one invariant"
    ]


def test_ledger_needs_a_database_target(tmp_path: Path) -> None:
    document = minimal_drill(target={"namespace": "shop"})
    assert errors_of(tmp_path, document) == [
        "spec: the write ledger and SQL invariants need target.cnpgCluster"
    ]


def test_unknown_fields_are_rejected(tmp_path: Path) -> None:
    document = minimal_drill(verfy={"upTo": "V2"})
    assert any(error.startswith("spec.verfy:") for error in errors_of(tmp_path, document))


def test_wrong_api_version_is_rejected(tmp_path: Path) -> None:
    document = minimal_drill() | {"apiVersion": "nostekon/v2"}
    assert any(error.startswith("apiVersion:") for error in errors_of(tmp_path, document))


def test_legacy_checkride_api_version_is_accepted(tmp_path: Path) -> None:
    document = minimal_drill() | {"apiVersion": "checkride/v1alpha1"}
    assert load_drill(write_drill(tmp_path, document)).api_version == "checkride/v1alpha1"


def test_lint_warns_about_weak_evidence(tmp_path: Path) -> None:
    document = minimal_drill(restore={"into": "namespace"}, objectives={})
    warnings = lint(load_drill(write_drill(tmp_path, document)))
    assert len(warnings) == 2
    assert "DORA Art. 12(3)" in warnings[0]


def test_lint_is_quiet_for_a_strong_drill() -> None:
    assert lint(load_drill(EXAMPLES / "mlflow-namespace-loss.yaml")) == []


def test_schema_uses_yaml_field_names_and_level_names() -> None:
    schema = drill_schema()
    assert {"apiVersion", "kind", "metadata", "spec"} <= set(schema["properties"])
    verify = schema["$defs"]["Verify"]["properties"]
    assert verify["upTo"]["enum"] == ["V0", "V1", "V2", "V3", "V4"]


def test_timeout_objective_parses_and_validates(tmp_path: Path) -> None:
    doc = minimal_drill(objectives={"rto": "15m", "rpo": "5m", "timeout": "1h"})
    drill = load_drill(write_drill(tmp_path, doc))
    assert drill.spec.objectives.timeout == timedelta(hours=1)

    bad_doc = minimal_drill(objectives={"rto": "15m", "timeout": "bad-duration"})
    errors = errors_of(tmp_path, bad_doc)
    assert any("spec.objectives.timeout" in err for err in errors)
