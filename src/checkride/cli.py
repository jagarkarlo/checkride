import argparse
from collections.abc import Sequence

from checkride import __version__
from checkride.levels import LEVELS


def _cmd_version(_: argparse.Namespace) -> int:
    print(__version__)
    return 0


def _cmd_levels(_: argparse.Namespace) -> int:
    for level, info in LEVELS.items():
        print(f"{level.name}  {info.question:<34}  {info.evidence}")
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

    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.handler(args)
