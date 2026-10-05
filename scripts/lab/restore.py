#!/usr/bin/env python3
"""Run a disposable PostgreSQL dump/restore across the two k3d lab clusters."""

import sys

from checkride.cli import main

if __name__ == "__main__":
    raise SystemExit(main(["lab", "run", *sys.argv[1:]]))
