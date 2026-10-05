"""Drill specifications: declarative descriptions of restore drills."""

import re
from datetime import datetime, timedelta
from enum import StrEnum
from pathlib import Path
from typing import Annotated, Any, Literal

import yaml
from pydantic import (
    BaseModel,
    BeforeValidator,
    ConfigDict,
    Field,
    PlainSerializer,
    ValidationError,
    WithJsonSchema,
    field_validator,
    model_validator,
)
from pydantic.alias_generators import to_camel

from checkride.levels import Level, parse_level

API_VERSION = "nostekon/v1alpha1"
LEGACY_API_VERSION = "checkride/v1alpha1"

_DNS_LABEL = r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
_DURATION = re.compile(r"(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?")


def parse_duration(value: str | int | timedelta) -> timedelta:
    """Parse compact durations such as 90s, 15m or 1h30m; integers are seconds."""
    if isinstance(value, timedelta):
        return value
    if isinstance(value, int) and not isinstance(value, bool):
        return timedelta(seconds=value)
    match = _DURATION.fullmatch(str(value).strip())
    if match is None or not any(match.groups()):
        raise ValueError(f"invalid duration {value!r}; use forms like 90s, 15m or 1h30m")
    hours, minutes, seconds = (int(group or 0) for group in match.groups())
    return timedelta(hours=hours, minutes=minutes, seconds=seconds)


def format_duration(value: timedelta) -> str:
    hours, rest = divmod(int(value.total_seconds()), 3600)
    minutes, seconds = divmod(rest, 60)
    units = ((hours, "h"), (minutes, "m"), (seconds, "s"))
    return "".join(f"{amount}{unit}" for amount, unit in units if amount) or "0s"


Duration = Annotated[
    timedelta,
    BeforeValidator(parse_duration),
    PlainSerializer(format_duration, return_type=str),
    WithJsonSchema(
        {"type": "string", "pattern": r"^(?=\d)(\d+h)?(\d+m)?(\d+s)?$", "examples": ["15m"]}
    ),
]

VerificationLevel = Annotated[
    Level,
    BeforeValidator(parse_level),
    PlainSerializer(lambda level: level.name, return_type=str),
    WithJsonSchema({"type": "string", "enum": [level.name for level in Level]}),
]

DnsLabel = Annotated[str, Field(pattern=_DNS_LABEL, max_length=63)]


class Scenario(StrEnum):
    NAMESPACE_LOSS = "namespace-loss"
    CLUSTER_LOSS = "cluster-loss"
    BAD_MIGRATION = "bad-migration"
    RANSOMWARE = "ransomware"
    LOST_SECRET = "lost-secret"
    STORAGE_CLASS_MISMATCH = "storage-class-mismatch"


class RestoreInto(StrEnum):
    SEPARATE_CLUSTER = "separate-cluster"
    NAMESPACE = "namespace"


class _Model(BaseModel):
    model_config = ConfigDict(
        alias_generator=to_camel, populate_by_name=True, extra="forbid", frozen=True
    )


class Metadata(_Model):
    name: DnsLabel


class Target(_Model):
    namespace: DnsLabel
    argocd_application: str | None = None
    cnpg_cluster: str | None = None


class Restore(_Model):
    into: RestoreInto = RestoreInto.SEPARATE_CLUSTER
    point_in_time: datetime | None = None

    @field_validator("point_in_time")
    @classmethod
    def _require_offset(cls, value: datetime | None) -> datetime | None:
        if value is not None and value.utcoffset() is None:
            raise ValueError(
                "pointInTime needs a UTC offset such as Z or +02:00; "
                "CloudNativePG reads timestamps without one as UTC"
            )
        return value


class Invariant(_Model):
    name: str
    sql: str
    expect: int | float | bool | str


class Verify(_Model):
    up_to: VerificationLevel = Level.V4
    ledger: bool = False
    invariants: tuple[Invariant, ...] = ()

    @model_validator(mode="after")
    def _v4_needs_evidence(self) -> "Verify":
        if self.up_to is Level.V4 and not (self.ledger or self.invariants):
            raise ValueError("V4 verification needs the write ledger or at least one invariant")
        return self


class Objectives(_Model):
    rto: Duration | None = None
    rpo: Duration | None = None
    timeout: Duration | None = None


class Drill(_Model):
    scenario: Scenario
    target: Target
    restore: Restore = Field(default_factory=Restore)
    verify: Verify
    objectives: Objectives = Field(default_factory=Objectives)

    @model_validator(mode="after")
    def _check_consistency(self) -> "Drill":
        if self.scenario is Scenario.CLUSTER_LOSS and self.restore.into is RestoreInto.NAMESPACE:
            raise ValueError("a cluster-loss drill must restore into a separate cluster")
        if (self.verify.ledger or self.verify.invariants) and not self.target.cnpg_cluster:
            raise ValueError("the write ledger and SQL invariants need target.cnpgCluster")
        return self


class DrillSpec(_Model):
    api_version: Literal["nostekon/v1alpha1", "checkride/v1alpha1"]
    kind: Literal["Drill"]
    metadata: Metadata
    spec: Drill


def load_drill(path: str | Path) -> DrillSpec:
    with Path(path).open(encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    return DrillSpec.model_validate(data)


def lint(drill: DrillSpec) -> list[str]:
    """Return warnings for valid drills that would give weak evidence."""
    warnings = []
    if drill.spec.restore.into is RestoreInto.NAMESPACE:
        warnings.append(
            "restoring into a namespace of the source cluster hides missing CRDs, operators "
            "and Secrets; DORA Art. 12(3) asks for segregated systems"
        )
    if drill.spec.objectives.rto is None or drill.spec.objectives.rpo is None:
        warnings.append("no RTO/RPO objectives; the drill can measure recovery but not judge it")
    return warnings


def describe_errors(error: ValidationError) -> list[str]:
    problems = []
    for item in error.errors():
        location = ".".join(str(part) for part in item["loc"]) or "<document>"
        problems.append(f"{location}: {item['msg'].removeprefix('Value error, ')}")
    return problems


def drill_schema() -> dict[str, Any]:
    return DrillSpec.model_json_schema(by_alias=True)
