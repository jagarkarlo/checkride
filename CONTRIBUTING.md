# Contributing to Nostekon

Thanks for your interest. Nostekon is in an early, fast-moving stage, so
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
make lab-up     # creates nostekon-source and nostekon-restore
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

## Releases

PyPI publication is public and irreversible. The trusted publisher for the
`nostekon` project must match all four values:

| Setting | Value |
|---|---|
| GitHub owner | `jagarkarlo` |
| Repository | `nostekon` |
| Workflow filename | `release.yml` |
| GitHub environment | `pypi` |

A pending publisher enables the first publication; it does not reserve the
project name. Configure the GitHub `pypi` environment with required approval
before publishing, and verify these settings in PyPI immediately before release.

Update `src/nostekon/__init__.py` to the intended version, pass the development
checks and hosted CI, then create a new matching `v` tag. Do not move or reuse
`v0.1.0`: it predates the rename. A tag push builds a GitHub pre-release but does
not publish to PyPI.

For an approved first release, manually run the **Release** workflow from
`main`, supply that existing tag and enable **publish-pypi**. It checks out the
exact tag and rejects pre-rename source trees. Confirm the workflow's built
Python version matches the tag before approving the `pypi` environment job.
Verify the published version and install it in a clean virtualenv after upload.
Do not store a PyPI API token in the repository; the workflow uses OIDC.

Workflow lint and checkout guards were checked locally on 2026-10-05. PyPI
account configuration and an end-to-end upload have not been independently
verified.

## Reporting bugs

Open an issue with the Nostekon version, the drill spec (without secrets), and
the command output. Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
