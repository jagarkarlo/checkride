# Contributing to Checkride

Thanks for your interest. Checkride is in an early, fast-moving stage, so
please open an issue before starting larger changes.

## Development setup

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -e ".[dev]"
make test
make lint
```

Python 3.12 or newer is required.

## Lab clusters

Drills run against disposable [k3d](https://k3d.io/) clusters:

```bash
make lab-up     # creates checkride-source and checkride-restore
make lab-down   # deletes both clusters
```

Never point drills at a production cluster. Drills delete namespaces, stop
databases and restore data by design.

## Pull requests

- Keep each pull request focused on one change.
- Add or update tests for every behaviour change.
- Run `make lint` and `make test` before pushing.
- Write commit messages in the
  [Conventional Commits](https://www.conventionalcommits.org/) style, for
  example `feat(ledger): report holes in recovered writes`.
- AI-assisted changes are welcome, but you must understand, review and test
  every line you submit.

## Reporting bugs

Open an issue with the Checkride version, the drill spec (without secrets), and
the command output. Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
