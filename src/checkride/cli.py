import argparse
import json
from collections.abc import Sequence

import yaml
from pydantic import ValidationError

from checkride import __version__
from checkride.levels import LEVELS
from checkride.spec import describe_errors, drill_schema, lint, load_drill


def _cmd_version(_: argparse.Namespace) -> int:
    print(__version__)
    return 0


def _cmd_levels(_: argparse.Namespace) -> int:
    for level, info in LEVELS.items():
        print(f"{level.name}  {info.question:<34}  {info.evidence}")
    return 0


def _cmd_validate(args: argparse.Namespace) -> int:
    failed = False
    for path in args.files:
        try:
            drill = load_drill(path)
        except OSError as error:
            problems = [error.strerror or str(error)]
        except yaml.YAMLError as error:
            problems = [f"invalid YAML: {error}"]
        except ValidationError as error:
            problems = describe_errors(error)
        else:
            for warning in lint(drill):
                print(f"warn  {path}: {warning}")
            print(f"ok    {path}")
            continue
        failed = True
        for problem in problems:
            print(f"error {path}: {problem}")
    return 1 if failed else 0


def _cmd_schema(_: argparse.Namespace) -> int:
    print(json.dumps(drill_schema(), indent=2))
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="checkride",
        description="Prove, don't assume, that you can recover.",
    )
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    commands = parser.add_subparsers(dest="command", required=True, metavar="COMMAND")

    version = commands.add_parser("version", help="print the Checkride version")
    version.set_defaults(handler=_cmd_version)

    levels = commands.add_parser("levels", help="list the verification levels V0-V4")
    levels.set_defaults(handler=_cmd_levels)

    validate = commands.add_parser("validate", help="validate drill spec files")
    validate.add_argument("files", nargs="+", metavar="FILE")
    validate.set_defaults(handler=_cmd_validate)

    schema = commands.add_parser("schema", help="print the drill spec JSON Schema")
    schema.set_defaults(handler=_cmd_schema)

    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.handler(args)
